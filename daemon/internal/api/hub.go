package api

import (
	"sync"
	"time"

	"PrismPanel-daemon/internal/protocol"
	"github.com/gorilla/websocket"
)

type controlClient struct {
	conn      *websocket.Conn
	source    string
	send      chan protocol.Outgoing
	done      chan struct{}
	closeOnce sync.Once
}

func newControlClient(conn *websocket.Conn, source string) *controlClient {
	return &controlClient{
		conn: conn, source: source, send: make(chan protocol.Outgoing, 256), done: make(chan struct{}),
	}
}

func (c *controlClient) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

func (c *controlClient) enqueue(message protocol.Outgoing) bool {
	select {
	case <-c.done:
		return false
	case c.send <- message:
		return true
	default:
		c.close()
		return false
	}
}

func (c *controlClient) writeLoop() {
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	defer c.close()
	for {
		select {
		case <-c.done:
			return
		case message := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteJSON(message); err != nil {
				return
			}
		case <-ping.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

type controlHub struct {
	mu      sync.RWMutex
	clients map[*controlClient]struct{}
}

func newControlHub() *controlHub {
	return &controlHub{clients: make(map[*controlClient]struct{})}
}

func (h *controlHub) add(client *controlClient) {
	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()
}

func (h *controlHub) remove(client *controlClient) {
	h.mu.Lock()
	delete(h.clients, client)
	h.mu.Unlock()
	client.close()
}

func (h *controlHub) broadcast(message protocol.Outgoing) {
	h.mu.RLock()
	clients := make([]*controlClient, 0, len(h.clients))
	for client := range h.clients {
		clients = append(clients, client)
	}
	h.mu.RUnlock()
	for _, client := range clients {
		if !client.enqueue(message) {
			h.remove(client)
		}
	}
}

func (h *controlHub) count() int {
	h.mu.RLock()
	count := len(h.clients)
	h.mu.RUnlock()
	return count
}

func (h *controlHub) close() {
	h.mu.Lock()
	clients := make([]*controlClient, 0, len(h.clients))
	for client := range h.clients {
		clients = append(clients, client)
		delete(h.clients, client)
	}
	h.mu.Unlock()
	for _, client := range clients {
		client.close()
	}
}
