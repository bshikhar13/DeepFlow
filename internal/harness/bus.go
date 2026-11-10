// Package harness runs DeepFlow's event routing, shared context and Cell Agents.
package harness

import (
	"sync"
	"time"

	"deepflow/internal/domain"
	"deepflow/internal/store"
)

// Bus persists events and fans them out to subscribers.
type Bus struct {
	store *store.Store
	mu    sync.Mutex
	subs  map[int]func(domain.Event)
	next  int
}

// NewBus returns a Bus backed by s.
func NewBus(s *store.Store) *Bus {
	return &Bus{store: s, subs: map[int]func(domain.Event){}}
}

// Publish persists e, then delivers it. A repeated dedupe key is a no-op.
// Subscribers run synchronously and must not block.
func (b *Bus) Publish(e domain.Event) error {
	if e.ID == "" {
		e.ID = domain.NewID("evt")
	}
	if e.DedupeKey == "" {
		e.DedupeKey = e.ID
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	inserted, err := b.store.AppendEvent(e)
	if err != nil || !inserted {
		return err
	}
	b.mu.Lock()
	subs := make([]func(domain.Event), 0, len(b.subs))
	for _, s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.Unlock()
	for _, s := range subs {
		s(e)
	}
	return nil
}

// Subscribe registers fn and returns a function that removes it.
func (b *Bus) Subscribe(fn func(domain.Event)) (cancel func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	b.subs[id] = fn
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subs, id)
	}
}
