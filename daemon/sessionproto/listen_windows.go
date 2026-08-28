//go:build windows

package sessionproto

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func Listen(socket string) (net.Listener, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { return nil, err }
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil { _ = listener.Close(); return nil, err }
	if err := os.MkdirAll(filepath.Dir(socket), 0o750); err != nil { _ = listener.Close(); return nil, err }
	if err := os.WriteFile(socket, []byte(port), 0o600); err != nil { _ = listener.Close(); return nil, err }
	return listener, nil
}

func Dial(socket string, timeout time.Duration) (net.Conn, error) {
	contents, err := os.ReadFile(socket)
	if err != nil { return nil, err }
	port := strings.TrimSpace(string(contents))
	if _, err := strconv.Atoi(port); err != nil { return nil, err }
	return (&net.Dialer{Timeout: timeout}).Dial("tcp", net.JoinHostPort("127.0.0.1", port))
}

func CleanupSocket(socket string) { if socket != "" { _ = os.Remove(socket) } }
