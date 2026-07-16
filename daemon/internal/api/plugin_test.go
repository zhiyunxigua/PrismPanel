package api

import "testing"

func TestPluginEndpointAcceptsOnlyLoopbackAddresses(t *testing.T) {
	for _, address := range []string{"127.0.0.1:1234", "[::1]:1234"} {
		if !isLoopbackRequest(address) {
			t.Fatalf("expected %s to be accepted", address)
		}
	}
	for _, address := range []string{"192.0.2.10:1234", "invalid"} {
		if isLoopbackRequest(address) {
			t.Fatalf("expected %s to be rejected", address)
		}
	}
}
