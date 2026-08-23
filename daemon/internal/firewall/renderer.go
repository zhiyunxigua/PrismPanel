package firewall

import (
	"fmt"
	"math"
	"net/netip"
	"sort"
	"strings"
	"time"
)

const firewallTableName = "prismpanel"

func renderScript(state persistedState, grants map[string]Grant, daemonPort int, tablePresent bool) (string, error) {
	var output strings.Builder
	if tablePresent {
		output.WriteString("delete table inet " + firewallTableName + "\n")
	}
	if !stateActive(state) {
		return output.String(), nil
	}

	controls4, controls6, err := splitPrefixes(state.System.ControlSources)
	if err != nil {
		return "", err
	}
	grants4, grants6, err := activeGrantElements(grants, time.Now().UTC())
	if err != nil {
		return "", err
	}

	output.WriteString("table inet " + firewallTableName + " {\n")
	if state.System.Enabled {
		writeStaticSet(&output, "prismpanel_control4", "ipv4_addr", controls4)
		writeStaticSet(&output, "prismpanel_control6", "ipv6_addr", controls6)
		writeTimeoutSet(&output, "prismpanel_direct_grants4", "ipv4_addr", grants4)
		writeTimeoutSet(&output, "prismpanel_direct_grants6", "ipv6_addr", grants6)
	}
	output.WriteString("  chain input {\n")
	output.WriteString("    type filter hook input priority filter; policy accept;\n")
	output.WriteString("    iifname \"lo\" accept\n")
	if state.System.Enabled {
		output.WriteString("    ct state established,related accept\n")
		output.WriteString(fmt.Sprintf("    ip saddr @prismpanel_control4 tcp dport %d accept\n", daemonPort))
		output.WriteString(fmt.Sprintf("    ip6 saddr @prismpanel_control6 tcp dport %d accept\n", daemonPort))
		output.WriteString(fmt.Sprintf("    ip saddr @prismpanel_direct_grants4 tcp dport %d accept\n", daemonPort))
		output.WriteString(fmt.Sprintf("    ip6 saddr @prismpanel_direct_grants6 tcp dport %d accept\n", daemonPort))
		output.WriteString(fmt.Sprintf("    tcp dport %d drop\n", daemonPort))
	}

	protected := map[string][]PortRange{"tcp": {}, "udp": {}}
	for _, rule := range state.Rules {
		if !rule.Enabled {
			continue
		}
		sources4, sources6, splitErr := splitPrefixes(rule.Sources)
		if splitErr != nil {
			return "", splitErr
		}
		for _, protocol := range rule.Protocols {
			ports := formatPorts(rule.Ports)
			if len(sources4) > 0 {
				output.WriteString(fmt.Sprintf("    ip saddr { %s } %s dport { %s } accept\n", strings.Join(sources4, ", "), protocol, ports))
			}
			if len(sources6) > 0 {
				output.WriteString(fmt.Sprintf("    ip6 saddr { %s } %s dport { %s } accept\n", strings.Join(sources6, ", "), protocol, ports))
			}
			protected[protocol] = append(protected[protocol], rule.Ports...)
		}
	}
	for _, protocol := range []string{"tcp", "udp"} {
		if len(protected[protocol]) == 0 {
			continue
		}
		ports := mergePortRanges(append([]PortRange(nil), protected[protocol]...))
		output.WriteString(fmt.Sprintf("    %s dport { %s } drop\n", protocol, formatPorts(ports)))
	}
	output.WriteString("  }\n")
	output.WriteString("}\n")
	return output.String(), nil
}

func stateActive(state persistedState) bool {
	if state.System.Enabled {
		return true
	}
	for _, rule := range state.Rules {
		if rule.Enabled {
			return true
		}
	}
	return false
}

func writeStaticSet(output *strings.Builder, name, addressType string, elements []string) {
	output.WriteString("  set " + name + " {\n")
	output.WriteString("    type " + addressType + "\n")
	output.WriteString("    flags interval\n")
	if len(elements) > 0 {
		output.WriteString("    elements = { " + strings.Join(elements, ", ") + " }\n")
	}
	output.WriteString("  }\n")
}

func writeTimeoutSet(output *strings.Builder, name, addressType string, elements []string) {
	output.WriteString("  set " + name + " {\n")
	output.WriteString("    type " + addressType + "\n")
	output.WriteString("    flags timeout\n")
	output.WriteString("    timeout " + formatDuration(DefaultGrantTTL) + "\n")
	if len(elements) > 0 {
		output.WriteString("    elements = { " + strings.Join(elements, ", ") + " }\n")
	}
	output.WriteString("  }\n")
}

func splitPrefixes(values []string) ([]string, []string, error) {
	var ipv4, ipv6 []netip.Prefix
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, nil, fmt.Errorf("parse normalized source %q: %w", value, err)
		}
		prefix = prefix.Masked()
		if prefix.Addr().Is4() {
			ipv4 = append(ipv4, prefix)
		} else {
			ipv6 = append(ipv6, prefix)
		}
	}
	return compactPrefixes(ipv4), compactPrefixes(ipv6), nil
}

func compactPrefixes(values []netip.Prefix) []string {
	sort.Slice(values, func(left, right int) bool {
		if values[left].Bits() != values[right].Bits() {
			return values[left].Bits() < values[right].Bits()
		}
		return values[left].Addr().Compare(values[right].Addr()) < 0
	})
	result := make([]netip.Prefix, 0, len(values))
	for _, candidate := range values {
		contained := false
		for _, existing := range result {
			if existing.Contains(candidate.Addr()) {
				contained = true
				break
			}
		}
		if !contained {
			result = append(result, candidate)
		}
	}
	formatted := make([]string, len(result))
	for index, prefix := range result {
		formatted[index] = prefix.String()
	}
	return formatted
}

func activeGrantElements(grants map[string]Grant, now time.Time) ([]string, []string, error) {
	remaining := make(map[netip.Addr]time.Duration)
	for _, grant := range grants {
		if !grant.ExpiresAt.After(now) {
			continue
		}
		address, err := netip.ParseAddr(grant.Source)
		if err != nil {
			return nil, nil, fmt.Errorf("parse grant source %q: %w", grant.Source, err)
		}
		address = address.Unmap()
		duration := grant.ExpiresAt.Sub(now)
		if duration > remaining[address] {
			remaining[address] = duration
		}
	}
	addresses := make([]netip.Addr, 0, len(remaining))
	for address := range remaining {
		addresses = append(addresses, address)
	}
	sort.Slice(addresses, func(left, right int) bool { return addresses[left].Compare(addresses[right]) < 0 })
	result4 := make([]string, 0, len(addresses))
	result6 := make([]string, 0, len(addresses))
	for _, address := range addresses {
		element := address.String() + " timeout " + formatDuration(remaining[address])
		if address.Is4() {
			result4 = append(result4, element)
		} else {
			result6 = append(result6, element)
		}
	}
	return result4, result6, nil
}

func formatDuration(value time.Duration) string {
	seconds := math.Max(1, math.Ceil(value.Seconds()))
	return fmt.Sprintf("%.0fs", seconds)
}

func formatPorts(values []PortRange) string {
	formatted := make([]string, len(values))
	for index, value := range values {
		if value.From == value.To {
			formatted[index] = fmt.Sprintf("%d", value.From)
		} else {
			formatted[index] = fmt.Sprintf("%d-%d", value.From, value.To)
		}
	}
	return strings.Join(formatted, ", ")
}
