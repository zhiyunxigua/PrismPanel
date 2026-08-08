package firewall

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"PrismPanel-daemon/internal/apperr"
)

type fakeFirewallBackend struct {
	contents string
	exists   bool
	applyLog []string
	adds     []netip.Addr
	removes  []netip.Addr
}

func (f *fakeFirewallBackend) Status(context.Context) backendStatus {
	return backendStatus{Supported: true, Name: "fake-nftables"}
}

func (f *fakeFirewallBackend) Inspect(context.Context) (string, bool, error) {
	return f.contents, f.exists, nil
}

func (f *fakeFirewallBackend) Apply(_ context.Context, script string) error {
	f.applyLog = append(f.applyLog, script)
	f.contents = script
	f.exists = true
	return nil
}

func (f *fakeFirewallBackend) AddGrant(_ context.Context, address netip.Addr, _ time.Duration) error {
	f.adds = append(f.adds, address)
	return nil
}

func (f *fakeFirewallBackend) RemoveGrant(_ context.Context, address netip.Addr) error {
	f.removes = append(f.removes, address)
	return nil
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(value []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(value)))
	return len(value), nil
}

func newTestFirewallService(t *testing.T, backend *fakeFirewallBackend) *Service {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	service, err := newService(t.TempDir()+"/firewall.json", 18443, backend, logger)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func firewallErrorCode(err error) string {
	var target *apperr.Error
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

func TestNormalizeRuleCanonicalizesValues(t *testing.T) {
	service := newTestFirewallService(t, &fakeFirewallBackend{})
	rule, err := service.normalizeRule("rule-a", RuleInput{
		Enabled:   true,
		Protocols: []string{"UDP", "tcp", "tcp", "icmp"},
		Ports:     []PortRange{{From: 3001, To: 3001}, {From: 3000, To: 3000}},
		Sources:   []string{"2001:db8::1", "192.0.2.10", "192.0.2.10"},
		Note:      " test ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rule.Protocols, ",") != "tcp,udp" {
		t.Fatalf("unexpected protocols: %#v", rule.Protocols)
	}
	if len(rule.Ports) != 1 || rule.Ports[0] != (PortRange{From: 3000, To: 3001}) {
		t.Fatalf("unexpected ports: %#v", rule.Ports)
	}
	if strings.Join(rule.Sources, ",") != "192.0.2.10/32,2001:db8::1/128" {
		t.Fatalf("unexpected sources: %#v", rule.Sources)
	}
	if rule.Note != "test" {
		t.Fatalf("unexpected note: %q", rule.Note)
	}
}

func TestFirewallRejectsProtectedPortOverlapAndStaleRevision(t *testing.T) {
	service := newTestFirewallService(t, &fakeFirewallBackend{})
	input := CreateRuleInput{Rule: RuleInput{
		Enabled:   true,
		Protocols: []string{"tcp"},
		Ports:     []PortRange{{From: 20000, To: 20000}},
		Sources:   []string{"192.0.2.0/24"},
	}}
	view, err := service.CreateRule(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status.Revision != 1 {
		t.Fatalf("revision = %d, want 1", view.Status.Revision)
	}
	if _, err := service.CreateRule(context.Background(), input); firewallErrorCode(err) != "FIREWALL_REVISION_CONFLICT" {
		t.Fatalf("stale revision error = %v", err)
	}
	input.ExpectedRevision = 1
	if _, err := service.CreateRule(context.Background(), input); firewallErrorCode(err) != "FIREWALL_RULE_CONFLICT" {
		t.Fatalf("overlap error = %v", err)
	}
	_, err = service.normalizeRule("protected", RuleInput{
		Enabled:   true,
		Protocols: []string{"tcp"},
		Ports:     []PortRange{{From: 18440, To: 18450}},
		Sources:   []string{"192.0.2.1"},
	})
	if firewallErrorCode(err) != "FIREWALL_PROTECTED_PORT" {
		t.Fatalf("protected port error = %v", err)
	}
}

func TestFirewallDetectsExternalDrift(t *testing.T) {
	backend := &fakeFirewallBackend{}
	service := newTestFirewallService(t, backend)
	view, err := service.CreateRule(context.Background(), CreateRuleInput{Rule: RuleInput{
		Enabled:   true,
		Protocols: []string{"udp"},
		Ports:     []PortRange{{From: 20001, To: 20001}},
		Sources:   []string{"198.51.100.0/24"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend.contents += "\n    tcp dport 9 accept\n"
	status := service.Status(context.Background())
	if !status.Drift || status.State != "DRIFT" {
		t.Fatalf("unexpected drift status: %#v", status)
	}
	_, err = service.DeleteRule(context.Background(), view.Rules[0].ID, DeleteRuleInput{
		ExpectedRevision: view.Status.Revision,
	})
	if firewallErrorCode(err) != "FIREWALL_DRIFT" {
		t.Fatalf("drift mutation error = %v", err)
	}
}

func TestGrantReuseAndSessionRevocation(t *testing.T) {
	backend := &fakeFirewallBackend{}
	service := newTestFirewallService(t, backend)
	view, err := service.ConfigureSystem(context.Background(), "", ConfigureSystemInput{
		System: SystemAccessInput{
			Enabled: true, ControlSources: []string{"198.51.100.10"}, GrantTTLSeconds: 600,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ session, ticket string }{
		{session: "session-a", ticket: "ticket-a"},
		{session: "session-b", ticket: "ticket-b"},
	} {
		if err := service.GrantDirectAccess(context.Background(), "203.0.113.7", item.session, item.ticket); err != nil {
			t.Fatal(err)
		}
	}
	if got := service.View(context.Background()).Status.GrantCount; got != 2 {
		t.Fatalf("grant count = %d, want 2", got)
	}
	if err := service.RevokeSessionGrants(context.Background(), "session-a"); err != nil {
		t.Fatal(err)
	}
	if len(backend.removes) != 0 {
		t.Fatalf("shared source was removed too early: %#v", backend.removes)
	}
	if err := service.RevokeSessionGrants(context.Background(), "session-b"); err != nil {
		t.Fatal(err)
	}
	if len(backend.removes) != 1 || backend.removes[0].String() != "203.0.113.7" {
		t.Fatalf("unexpected removals: %#v", backend.removes)
	}
	if service.View(context.Background()).Status.Revision != view.Status.Revision {
		t.Fatal("temporary grants unexpectedly changed revision")
	}
}

func TestRenderScriptKeepsEstablishedConnectionsAndTimeoutGrants(t *testing.T) {
	state := persistedState{SchemaVersion: SchemaVersion, System: SystemAccess{
		Enabled: true, ControlSources: []string{"198.51.100.10/32"}, GrantTTLSeconds: 600,
	}}
	script, err := renderScript(state, map[string]Grant{
		"grant": {Source: "203.0.113.7", ExpiresAt: time.Now().Add(time.Minute)},
	}, 18443, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"ct state established,related accept",
		"flags timeout",
		"203.0.113.7 timeout",
		"tcp dport 18443 drop",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("rendered script missing %q:\n%s", expected, script)
		}
	}
}

func TestNormalizeObservedTableIgnoresOnlyGrantElements(t *testing.T) {
	base := "table inet prismpanel {\n" +
		" set prismpanel_direct_grants4 {\n" +
		"  type ipv4_addr\n" +
		"  flags timeout\n" +
		"  timeout 10m\n" +
		"  elements = { 203.0.113.7 timeout 10m }\n" +
		" }\n" +
		" chain input {\n" +
		"  type filter hook input priority filter; policy accept;\n" +
		" }\n" +
		"}"
	changedGrant := strings.Replace(base, "203.0.113.7 timeout 10m", "203.0.113.8 timeout 1m", 1)
	if normalizeObservedTable(base) != normalizeObservedTable(changedGrant) {
		t.Fatal("dynamic grant elements should not change the desired fingerprint")
	}
	changedTimeout := strings.Replace(base, "timeout 10m", "timeout 20m", 1)
	if normalizeObservedTable(base) == normalizeObservedTable(changedTimeout) {
		t.Fatal("dynamic set configuration changes must be detected")
	}
}
