package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func (s *Server) requestSourceIP(request *http.Request) string {
	remote, ok := requestAddressIP(request.RemoteAddr)
	if !ok {
		return ""
	}
	forwarded := strings.TrimSpace(request.Header.Get("X-Forwarded-For"))
	if forwarded == "" {
		return remote.String()
	}
	if !s.config.Security.IsTrustedProxy(remote) {
		return ""
	}
	forwardedAddresses := strings.Split(forwarded, ",")
	for index := len(forwardedAddresses) - 1; index >= 0; index-- {
		candidate, valid := parseAddressIP(forwardedAddresses[index])
		if !valid {
			return ""
		}
		if !s.config.Security.IsTrustedProxy(candidate) {
			return candidate.String()
		}
	}
	return ""
}

func requestAddressIP(remoteAddress string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		return netip.Addr{}, false
	}
	return parseAddressIP(host)
}

func parseAddressIP(value string) (netip.Addr, bool) {
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil || !address.IsValid() {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}
