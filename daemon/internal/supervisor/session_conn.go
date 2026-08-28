package supervisor

import (
	"bufio"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"PrismPanel-daemon/sessionproto"
)

type sessionConn struct {
	mu       sync.Mutex
	conn     net.Conn
	br       *bufio.Reader
	writer   *bufio.Writer
	hello    sessionproto.Frame
	done     chan struct{}
	wait     error
	once     sync.Once
	detached bool
}

func dialSession(socket, token string, timeout time.Duration) (*sessionConn, error) {
	conn, err := sessionproto.Dial(socket, timeout)
	if err != nil {
		return nil, err
	}
	client := &sessionConn{conn: conn, br: bufio.NewReader(conn), writer: bufio.NewWriter(conn), done: make(chan struct{})}
	if err := client.write(sessionproto.Frame{Type: sessionproto.TypeHello, Token: token}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	hello, err := sessionproto.ReadFrame(client.br)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if hello.Type == sessionproto.TypeError {
		_ = conn.Close()
		return nil, errors.New(hello.Error)
	}
	if hello.Type != sessionproto.TypeHello || hello.PID <= 0 || hello.Session == "" {
		_ = conn.Close()
		return nil, errors.New("session handshake is invalid")
	}
	client.hello = hello
	return client, nil
}

func (c *sessionConn) write(frame sessionproto.Frame) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return io.ErrClosedPipe
	}
	if err := sessionproto.WriteFrame(c.writer, frame); err != nil {
		return err
	}
	return c.writer.Flush()
}

func (c *sessionConn) attach(after uint64) error {
	return c.write(sessionproto.Frame{Type: sessionproto.TypeAttach, After: after})
}

func (c *sessionConn) writeStdin(value string) error {
	return c.write(sessionproto.Frame{Type: sessionproto.TypeStdin, Content: value})
}

func (c *sessionConn) signal(name string) error {
	return c.write(sessionproto.Frame{Type: sessionproto.TypeSignal, Signal: name})
}

func (c *sessionConn) reader() *bufio.Reader {
	return c.br
}

func (c *sessionConn) close() {
	c.once.Do(func() {
		if c.conn != nil {
			_ = c.conn.Close()
		}
		close(c.done)
	})
}

func (c *sessionConn) markDetached() {
	c.mu.Lock()
	c.detached = true
	c.mu.Unlock()
	c.close()
}

func (c *sessionConn) Detached() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.detached
}

func (c *sessionConn) Done() <-chan struct{} {
	return c.done
}

func (c *sessionConn) markExit(err error) {
	c.mu.Lock()
	c.wait = err
	c.mu.Unlock()
	c.close()
}

func (c *sessionConn) Wait() error {
	<-c.done
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wait
}
