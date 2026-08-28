//go:build !windows

package sessionproto

import (
	"net"
	"os"
	"path/filepath"
	"time"
)

func Listen(socket string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(socket), 0o750); err != nil { return nil, err }
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil { return nil, err }
	_ = os.Chmod(socket, 0o660)
	return listener, nil
}

func Dial(socket string, timeout time.Duration) (net.Conn, error) {
	return (&net.Dialer{Timeout: timeout}).Dial("unix", socket)
}

func CleanupSocket(socket string) { if socket != "" { _ = os.Remove(socket) } }
