package harness

import (
	"path/filepath"
	"testing"

	"deepflow/internal/domain"
	"deepflow/internal/store"
)

func busRig(t *testing.T) (*Bus, *store.Store, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p, _ := st.CreatePursuit("t", "", 8)
	s, _ := st.CreateSession(p.ID, "")
	return NewBus(st), st, s.ID
}

func TestBusPersistsBeforeDeliver(t *testing.T) {
	b, st, sid := busRig(t)
	var seen bool
	b.Subscribe(func(e domain.Event) {
		if _, err := st.GetEvent(e.ID); err == nil {
			seen = true
		}
	})
	if err := b.Publish(domain.Event{SessionID: sid, Type: domain.EvUserMessage, DedupeKey: "a"}); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("event was not persisted before delivery")
	}
}

func TestBusDropsDuplicates(t *testing.T) {
	b, _, sid := busRig(t)
	n := 0
	cancel := b.Subscribe(func(e domain.Event) { n++ })
	for i := 0; i < 2; i++ {
		if err := b.Publish(domain.Event{SessionID: sid, Type: domain.EvUserMessage, DedupeKey: "same"}); err != nil {
			t.Fatal(err)
		}
	}
	if n != 1 {
		t.Fatalf("delivered %d", n)
	}
	cancel()
	b.Publish(domain.Event{SessionID: sid, Type: domain.EvUserMessage, DedupeKey: "other"})
	if n != 1 {
		t.Fatal("delivered after cancel")
	}
}

func TestBusAssignsDedupeKeyWhenMissing(t *testing.T) {
	b, _, sid := busRig(t)
	n := 0
	b.Subscribe(func(e domain.Event) { n++ })
	b.Publish(domain.Event{SessionID: sid, Type: domain.EvUserMessage})
	b.Publish(domain.Event{SessionID: sid, Type: domain.EvUserMessage})
	if n != 2 {
		t.Fatalf("delivered %d", n)
	}
}
