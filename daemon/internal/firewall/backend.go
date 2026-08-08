package firewall

import (
	"context"
	"net/netip"
	"time"
)

type backendStatus struct {
	Supported bool
	Reason    string
	Name      string
}

type backend interface {
	Status(context.Context) backendStatus
	Inspect(context.Context) (string, bool, error)
	Apply(context.Context, string) error
	AddGrant(context.Context, netip.Addr, time.Duration) error
	RemoveGrant(context.Context, netip.Addr) error
}
