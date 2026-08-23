package eventbus

import (
	"sync"

	"PrismPanel-daemon/internal/protocol"
)

type Bus struct {
	mu       sync.RWMutex
	handlers []func(protocol.Outgoing)
}

func (b *Bus) Subscribe(handler func(protocol.Outgoing)) {
	b.mu.Lock()
	b.handlers = append(b.handlers, handler)
	b.mu.Unlock()
}

func (b *Bus) Publish(event protocol.Outgoing) {
	b.mu.RLock()
	handlers := append([]func(protocol.Outgoing){}, b.handlers...)
	b.mu.RUnlock()
	for _, handler := range handlers {
		handler(event)
	}
}
