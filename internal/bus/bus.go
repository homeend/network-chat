// Package bus is a tiny in-process pub/sub for protocol events.
package bus

import (
	"sync"

	"netchat/internal/protocol"
)

type Bus struct {
	mu   sync.Mutex
	subs map[chan protocol.Event]struct{}
}

func New() *Bus { return &Bus{subs: map[chan protocol.Event]struct{}{}} }

// Subscribe returns a buffered channel of events and a cancel func.
// Slow subscribers drop events (SSE clients recover by re-fetching by seq).
func (b *Bus) Subscribe() (<-chan protocol.Event, func()) {
	ch := make(chan protocol.Event, 256)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

func (b *Bus) Publish(e protocol.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}
