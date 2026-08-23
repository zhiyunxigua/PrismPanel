//go:build !linux

package firewall

import (
	"context"
	"net/netip"
	"runtime"
	"time"
)

type unsupportedBackend struct{}

func newPlatformBackend() backend {
	return unsupportedBackend{}
}

func (unsupportedBackend) Status(context.Context) backendStatus {
	return backendStatus{
		Reason: "当前操作系统 " + runtime.GOOS + " 不支持 nftables 网络白名单管理",
		Name:   "unsupported",
	}
}

func (unsupportedBackend) Inspect(context.Context) (string, bool, error) {
	return "", false, nil
}

func (unsupportedBackend) Apply(context.Context, string) error {
	return nil
}

func (unsupportedBackend) AddGrant(context.Context, netip.Addr, time.Duration) error {
	return nil
}

func (unsupportedBackend) RemoveGrant(context.Context, netip.Addr) error {
	return nil
}
