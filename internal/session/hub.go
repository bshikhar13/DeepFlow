package session

import (
	"encoding/json"
	"sync"
)

const hubBuffer = 1024

// HubEvent is one server-sent event.
type HubEvent struct {
	Seq  int64
	Name string
	Data json.RawMessage
}

// hub assigns per-session sequence numbers, keeps recent events for replay and
// fans them out. A client that falls behind is told to resync.
type hub struct {
	mu   sync.Mutex
	seq  int64
	buf  []HubEvent
	subs map[chan HubEvent]struct{}
}

func newHub() *hub { return &hub{subs: map[chan HubEvent]struct{}{}} }

func (h *hub) publish(name string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	ev := HubEvent{Seq: h.seq, Name: name, Data: b}
	h.buf = append(h.buf, ev)
	if len(h.buf) > hubBuffer {
		h.buf = h.buf[len(h.buf)-hubBuffer:]
	}
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
			// Too slow: drop the client. The stream closes and it resyncs on reconnect.
			delete(h.subs, ch)
			close(ch)
		}
	}
}

// Subscribe returns events after lastSeq. needResync is true when those events
// can't all be replayed. The returned cancel must be called.
func (h *hub) subscribe(lastSeq int64) (replay []HubEvent, needResync bool, ch chan HubEvent, cancel func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch = make(chan HubEvent, 256)
	h.subs[ch] = struct{}{}
	cancel = func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
	}
	if lastSeq > h.seq {
		return nil, true, ch, cancel
	}
	if lastSeq > 0 && len(h.buf) > 0 && h.buf[0].Seq > lastSeq+1 {
		return nil, true, ch, cancel
	}
	for _, e := range h.buf {
		if e.Seq > lastSeq {
			replay = append(replay, e)
		}
	}
	return replay, false, ch, cancel
}

func (h *hub) current() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seq
}

// CurrentSeq returns the latest stream sequence number.
func (l *Live) CurrentSeq() int64 { return l.hub.current() }
