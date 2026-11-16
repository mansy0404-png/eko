// Package bus is a tiny in-process pub/sub used to fan out live events to the
// control UI over server-sent events.
package bus

import "sync"

type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type Bus struct {
	mu   sync.Mutex
	next int
	subs map[int]chan Event
}

func New() *Bus {
	return &Bus{subs: make(map[int]chan Event)}
}

func (b *Bus) Subscribe() (int, <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	id := b.next
	ch := make(chan Event, 128)
	b.subs[id] = ch
	return id, ch
}

func (b *Bus) Unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[id]; ok {
		delete(b.subs, id)
		close(ch)
	}
}

// Publish never blocks: a slow UI drops events instead of stalling the proxy.
func (b *Bus) Publish(typ string, data any) {
	ev := Event{Type: typ, Data: data}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}
