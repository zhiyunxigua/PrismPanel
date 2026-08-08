package config

import (
	"net/netip"
	"testing"
)

func TestTrustedProxyCIDRsNormalizeAndMatch(t *testing.T) {
	cfg := Default()
	cfg.Security.TrustedProxyCIDRs = []string{"10.0.0.1/24", "2001:db8::1"}
	if err := cfg.Security.Validate(); err != nil {
		t.Fatal(err)
	}
	if !cfg.Security.IsTrustedProxy(netip.MustParseAddr("10.0.0.200")) {
		t.Fatal("expected IPv4 proxy CIDR to match")
	}
	if !cfg.Security.IsTrustedProxy(netip.MustParseAddr("2001:db8::1")) {
		t.Fatal("expected IPv6 proxy address to match")
	}
}

func TestTrustedProxyCIDRsRejectInvalidAndDuplicateValues(t *testing.T) {
	cfg := Default()
	cfg.Security.TrustedProxyCIDRs = []string{"10.0.0.0/33"}
	if err := cfg.Security.Validate(); err == nil {
		t.Fatal("accepted invalid proxy CIDR")
	}
	cfg.Security.TrustedProxyCIDRs = []string{"10.0.0.1/24", "10.0.0.0/24"}
	if err := cfg.Security.Validate(); err == nil {
		t.Fatal("accepted duplicate normalized proxy CIDR")
	}
}
