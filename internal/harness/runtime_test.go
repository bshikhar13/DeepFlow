package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deepflow/internal/domain"
	"deepflow/internal/provider"
	"deepflow/internal/store"
)

type fakeAgent struct {
	handle func(ctx context.Context, in AgentInput, ev domain.Event) (AgentOutput, error)
	init   func(ctx context.Context, in AgentInput) (AgentOutput, error)
	update func(ctx context.Context, in AgentInput, d Directive) (AgentOutput, error)
	typ    domain.CellType
}

func (a *fakeAgent) Type() domain.CellType { return a.typ }
func (a *fakeAgent) Init(ctx context.Context, in AgentInput) (AgentOutput, error) {
	if a.init != nil {
		return a.init(ctx, in)
	}
	return AgentOutput{Content: json.RawMessage(`{"v":"init"}`)}, nil
}
func (a *fakeAgent) Handle(ctx context.Context, in AgentInput, ev domain.Event) (AgentOutput, error) {
	return a.handle(ctx, in, ev)
}
func (a *fakeAgent) Update(ctx context.Context, in AgentInput, d Directive) (AgentOutput, error) {
	return a.update(ctx, in, d)
}
func (a *fakeAgent) Retain(content, state json.RawMessage) []domain.Artifact {
	return []domain.Artifact{{Content: state, LearnerAuthored: len(state) > 0}}
}

type recSink struct {
	mu      sync.Mutex
	updated []domain.Cell
	status  map[string]domain.CellStatus
	deltas  []string
	reqs    []CellRequest
}

func newSink() *recSink { return &recSink{status: map[string]domain.CellStatus{}} }
func (s *recSink) CellUpdated(c domain.Cell) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updated = append(s.updated, c)
	s.status[c.ID] = c.Status
}
func (s *recSink) CellStatus(id string, st domain.CellStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status[id] = st
}
func (s *recSink) Delta(id, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deltas = append(s.deltas, text)
}
func (s *recSink) Request(id string, r CellRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r)
}
func (s *recSink) statusOf(id string) domain.CellStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status[id]
}
func (s *recSink) lastContent(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.updated) - 1; i >= 0; i-- {
		if s.updated[i].ID == id {
			return string(s.updated[i].Content)
		}
	}
	return ""
}
func (s *recSink) updates() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.updated)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	dl := time.Now().Add(3 * time.Second)
	for time.Now().Before(dl) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type rig struct {
	r     *Runtime
	sink  *recSink
	agent *fakeAgent
	mock  *provider.Mock
	store *store.Store
	sess  string
}

func newRig(t *testing.T, mod func(*RuntimeConfig)) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p, _ := st.CreatePursuit("Topic", "", 8)
	sess, _ := st.CreateSession(p.ID, "")
	g := &rig{sink: newSink(), agent: &fakeAgent{typ: domain.CellConcept}, mock: provider.NewMock(), store: st, sess: sess.ID}
	cfg := RuntimeConfig{SessionID: sess.ID, Provider: g.mock, MaxConcurrent: 3, AgentBudget: 40, TaskTimeout: time.Second}
	if mod != nil {
		mod(&cfg)
	}
	g.r = NewRuntime(cfg, NewBus(st), NewContextStore(LearningContext{}), func(domain.CellType) Agent { return g.agent }, g.sink)
	t.Cleanup(g.r.Stop)
	return g
}

func (g *rig) start(t *testing.T, id string) {
	t.Helper()
	g.r.Start(domain.Cell{ID: id, Type: domain.CellConcept, AgentInstanceID: "a_" + id})
	waitFor(t, "init", func() bool { return g.sink.statusOf(id) == domain.StatusWaiting })
}

func ev(k string) domain.Event {
	return domain.Event{Type: domain.EvConceptExplored, DedupeKey: k}
}

func TestResultDroppedAfterRetire(t *testing.T) {
	g := newRig(t, nil)
	g.start(t, "c1")
	started := make(chan struct{})
	g.agent.handle = func(ctx context.Context, in AgentInput, e domain.Event) (AgentOutput, error) {
		close(started)
		<-ctx.Done()
		time.Sleep(5 * time.Millisecond)
		return AgentOutput{Content: json.RawMessage(`{"v":"late"}`)}, nil
	}
	g.r.Dispatch("c1", ev("1"))
	<-started
	n := g.sink.updates()
	if arts := g.r.Retire("c1"); len(arts) != 1 {
		t.Fatalf("artifacts %v", arts)
	}
	time.Sleep(30 * time.Millisecond)
	if g.sink.updates() != n {
		t.Fatal("retired agent updated the workspace")
	}
	g.r.Dispatch("c1", ev("2")) // accepting no more work; must not panic
}

func TestDeltaFromRetiredInstanceDropped(t *testing.T) {
	g := newRig(t, nil)
	g.start(t, "c1")
	g.agent.handle = func(ctx context.Context, in AgentInput, e domain.Event) (AgentOutput, error) {
		in.Delta("first")
		g.r.Retire("c1")
		in.Delta("late")
		return AgentOutput{}, nil
	}
	g.r.Dispatch("c1", ev("1"))
	waitFor(t, "first delta", func() bool { g.sink.mu.Lock(); defer g.sink.mu.Unlock(); return len(g.sink.deltas) >= 1 })
	time.Sleep(20 * time.Millisecond)
	g.sink.mu.Lock()
	defer g.sink.mu.Unlock()
	if len(g.sink.deltas) != 1 {
		t.Fatalf("deltas %v", g.sink.deltas)
	}
}

func TestResultDroppedAfterUserRevisionChange(t *testing.T) {
	g := newRig(t, nil)
	g.start(t, "c1")
	started, release := make(chan struct{}), make(chan struct{})
	g.agent.handle = func(ctx context.Context, in AgentInput, e domain.Event) (AgentOutput, error) {
		close(started)
		<-release
		return AgentOutput{Content: json.RawMessage(`{"v":"stale"}`)}, nil
	}
	g.r.Dispatch("c1", ev("1"))
	<-started
	g.r.BumpUserRevision("c1")
	close(release)
	waitFor(t, "back to waiting", func() bool { return g.sink.statusOf("c1") == domain.StatusWaiting })
	if got := g.sink.lastContent("c1"); got != `{"v":"init"}` {
		t.Fatalf("stale overwrote: %s", got)
	}
}

func TestOlderTaskCannotOverwriteNewer(t *testing.T) {
	g := newRig(t, nil)
	g.start(t, "c1")
	g.agent.handle = func(ctx context.Context, in AgentInput, e domain.Event) (AgentOutput, error) {
		if e.DedupeKey == "slow" {
			time.Sleep(20 * time.Millisecond)
		}
		return AgentOutput{Content: json.RawMessage(fmt.Sprintf(`{"v":%q}`, e.DedupeKey))}, nil
	}
	g.r.Dispatch("c1", ev("slow"))
	g.r.Dispatch("c1", ev("fast"))
	waitFor(t, "newest applied", func() bool { return g.sink.lastContent("c1") == `{"v":"fast"}` })
	time.Sleep(30 * time.Millisecond)
	if got := g.sink.lastContent("c1"); got != `{"v":"fast"}` {
		t.Fatalf("got %s", got)
	}
}

func TestPanicMarksOnlyThatCellFailed(t *testing.T) {
	g := newRig(t, nil)
	g.start(t, "bad")
	g.start(t, "good")
	g.agent.handle = func(ctx context.Context, in AgentInput, e domain.Event) (AgentOutput, error) {
		if in.Cell.ID == "bad" {
			panic("boom")
		}
		return AgentOutput{Content: json.RawMessage(`{"v":"ok"}`)}, nil
	}
	g.r.Dispatch("bad", ev("1"))
	waitFor(t, "failed", func() bool { return g.sink.statusOf("bad") == domain.StatusFailed })
	g.r.Dispatch("good", ev("2"))
	waitFor(t, "good responds", func() bool { return g.sink.lastContent("good") == `{"v":"ok"}` })
	if g.sink.lastContent("bad") != `{"v":"init"}` {
		t.Fatalf("lost last good content: %s", g.sink.lastContent("bad"))
	}
}

func TestProviderErrorKeepsLastContent(t *testing.T) {
	for _, e := range []error{provider.ErrMalformed, provider.ErrTruncated, provider.ErrRefused} {
		g := newRig(t, nil)
		g.start(t, "c1")
		g.agent.handle = func(ctx context.Context, in AgentInput, ev domain.Event) (AgentOutput, error) {
			return AgentOutput{}, fmt.Errorf("wrapped: %w", e)
		}
		g.r.Dispatch("c1", ev("1"))
		waitFor(t, "failed", func() bool { return g.sink.statusOf("c1") == domain.StatusFailed })
		if g.sink.lastContent("c1") != `{"v":"init"}` {
			t.Fatalf("%v: lost content", e)
		}
	}
}

func TestRetryReplaysFailedWork(t *testing.T) {
	g := newRig(t, nil)
	g.start(t, "c1")
	var n atomic.Int32
	g.agent.handle = func(ctx context.Context, in AgentInput, e domain.Event) (AgentOutput, error) {
		if n.Add(1) == 1 {
			return AgentOutput{}, errors.New("transient")
		}
		return AgentOutput{Content: json.RawMessage(`{"v":"retried"}`)}, nil
	}
	g.r.Dispatch("c1", ev("1"))
	waitFor(t, "failed", func() bool { return g.sink.statusOf("c1") == domain.StatusFailed })
	g.r.Retry("c1")
	waitFor(t, "retried", func() bool { return g.sink.lastContent("c1") == `{"v":"retried"}` })
}

func TestSemaphoreCapsConcurrency(t *testing.T) {
	g := newRig(t, nil)
	g.mock.Delay("n", 20*time.Millisecond)
	var cur, peak atomic.Int32
	g.agent.handle = func(ctx context.Context, in AgentInput, e domain.Event) (AgentOutput, error) {
		var out map[string]any
		// Track concurrency inside the governed call via wrapping scheduler acquire.
		release, err := g.r.Scheduler().Acquire(ctx, false)
		if err != nil {
			return AgentOutput{}, err
		}
		defer release()
		c := cur.Add(1)
		defer cur.Add(-1)
		for {
			p := peak.Load()
			if c <= p || peak.CompareAndSwap(p, c) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		_ = out
		return AgentOutput{Content: json.RawMessage(`{"v":"x"}`)}, nil
	}
	ids := []string{"a", "b", "c", "d", "e", "f"}
	for _, id := range ids {
		g.start(t, id)
	}
	for _, id := range ids {
		g.r.Dispatch(id, ev("k"+id))
	}
	waitFor(t, "all done", func() bool {
		for _, id := range ids {
			if g.sink.lastContent(id) != `{"v":"x"}` {
				return false
			}
		}
		return true
	})
	if peak.Load() > 3 {
		t.Fatalf("peak %d", peak.Load())
	}
}

func TestDialogueNotQueuedBehindCells(t *testing.T) {
	s := NewScheduler(1)
	ctx := context.Background()
	rel, _ := s.Acquire(ctx, false)
	order := make(chan string, 2)
	go func() {
		r, _ := s.Acquire(ctx, false)
		order <- "low"
		r()
	}()
	waitFor(t, "low queued", func() bool { return s.waiting() == 1 })
	go func() {
		r, _ := s.Acquire(ctx, true)
		order <- "high"
		r()
	}()
	waitFor(t, "high queued", func() bool { return s.waiting() == 2 })
	rel()
	if first := <-order; first != "high" {
		t.Fatalf("first %s", first)
	}
}

func TestBudgetExhaustedKeepsContent(t *testing.T) {
	g := newRig(t, func(c *RuntimeConfig) { c.AgentBudget = 2 })
	g.agent.init = func(ctx context.Context, in AgentInput) (AgentOutput, error) {
		return AgentOutput{Content: json.RawMessage(`{"v":"init"}`)}, nil
	}
	g.start(t, "c1")
	g.agent.handle = func(ctx context.Context, in AgentInput, e domain.Event) (AgentOutput, error) {
		var out map[string]any
		_, err := in.Provider.Structured(ctx, provider.StructuredRequest{Name: "concept.content"}, &out)
		if err != nil {
			return AgentOutput{}, err
		}
		return AgentOutput{Content: json.RawMessage(fmt.Sprintf(`{"v":%q}`, e.DedupeKey))}, nil
	}
	for _, k := range []string{"1", "2", "3"} {
		g.r.Dispatch("c1", ev(k))
	}
	waitFor(t, "budget message", func() bool {
		g.sink.mu.Lock()
		defer g.sink.mu.Unlock()
		n := len(g.sink.updated)
		return n > 0 && g.sink.updated[n-1].Error != "" && g.sink.updated[n-1].Status == domain.StatusWaiting
	})
	if g.mock.Calls("concept.content") != 2 {
		t.Fatalf("calls %d", g.mock.Calls("concept.content"))
	}
	if g.sink.lastContent("c1") != `{"v":"2"}` {
		t.Fatalf("content %s", g.sink.lastContent("c1"))
	}
}

func TestDialogueBudgetExempt(t *testing.T) {
	g := newRig(t, func(c *RuntimeConfig) { c.AgentBudget = 1 })
	g.agent.typ = domain.CellDialogue
	g.r.Start(domain.Cell{ID: "d", Type: domain.CellDialogue})
	waitFor(t, "init", func() bool { return g.sink.statusOf("d") == domain.StatusWaiting })
	var done atomic.Int32
	g.agent.handle = func(ctx context.Context, in AgentInput, e domain.Event) (AgentOutput, error) {
		_, err := in.Provider.Stream(ctx, provider.TextRequest{}, func(string) {})
		done.Add(1)
		return AgentOutput{}, err
	}
	g.r.Dispatch("d", ev("1"))
	g.r.Dispatch("d", ev("2"))
	waitFor(t, "two calls", func() bool { return g.mock.Calls(provider.StreamName) == 2 && done.Load() == 2 })
	if g.sink.statusOf("d") == domain.StatusFailed {
		t.Fatal("dialogue must not hit budget")
	}
}

func TestTaskTimeoutMarksFailed(t *testing.T) {
	g := newRig(t, func(c *RuntimeConfig) { c.TaskTimeout = 10 * time.Millisecond })
	g.start(t, "c1")
	g.agent.handle = func(ctx context.Context, in AgentInput, e domain.Event) (AgentOutput, error) {
		<-ctx.Done()
		return AgentOutput{}, ctx.Err()
	}
	g.r.Dispatch("c1", ev("1"))
	waitFor(t, "failed", func() bool { return g.sink.statusOf("c1") == domain.StatusFailed })
}

func TestCancelCurrentIsNotFailure(t *testing.T) {
	g := newRig(t, nil)
	g.start(t, "c1")
	started := make(chan struct{})
	g.agent.handle = func(ctx context.Context, in AgentInput, e domain.Event) (AgentOutput, error) {
		close(started)
		<-ctx.Done()
		return AgentOutput{}, ctx.Err()
	}
	g.r.Dispatch("c1", ev("1"))
	<-started
	g.r.CancelCurrent("c1")
	time.Sleep(20 * time.Millisecond)
	waitFor(t, "waiting", func() bool { return g.sink.statusOf("c1") == domain.StatusWaiting })
}

func TestSuspendedCellQueuesNoWork(t *testing.T) {
	g := newRig(t, nil)
	g.start(t, "c1")
	var calls atomic.Int32
	g.agent.handle = func(ctx context.Context, in AgentInput, e domain.Event) (AgentOutput, error) {
		calls.Add(1)
		return AgentOutput{}, nil
	}
	g.r.SetSuspended("c1", true)
	g.r.Dispatch("c1", ev("1"))
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("suspended agent ran")
	}
	if g.sink.statusOf("c1") != domain.StatusSuspended {
		t.Fatalf("status %s", g.sink.statusOf("c1"))
	}
	g.r.SetSuspended("c1", false)
	g.r.Dispatch("c1", ev("2"))
	waitFor(t, "resumed", func() bool { return calls.Load() == 1 })
}

func TestRestoredCellSkipsInit(t *testing.T) {
	g := newRig(t, nil)
	var inits atomic.Int32
	g.agent.init = func(ctx context.Context, in AgentInput) (AgentOutput, error) {
		inits.Add(1)
		return AgentOutput{}, nil
	}
	g.r.Start(domain.Cell{ID: "c1", Type: domain.CellConcept, Content: json.RawMessage(`{"v":"saved"}`)})
	time.Sleep(20 * time.Millisecond)
	if inits.Load() != 0 {
		t.Fatal("re-initialized a restored cell")
	}
}

func TestOutputPublishesEventsAndRequests(t *testing.T) {
	g := newRig(t, nil)
	g.start(t, "c1")
	g.agent.handle = func(ctx context.Context, in AgentInput, e domain.Event) (AgentOutput, error) {
		return AgentOutput{
			Publish:  []domain.Event{{Type: domain.EvQuizResult, DedupeKey: "out1"}},
			Requests: []CellRequest{{Kind: "replace", Reason: "done"}},
		}, nil
	}
	g.r.Dispatch("c1", ev("1"))
	waitFor(t, "request", func() bool { g.sink.mu.Lock(); defer g.sink.mu.Unlock(); return len(g.sink.reqs) == 1 })
	evs, err := g.store.ListEvents(g.sess, domain.EvQuizResult)
	if err != nil || len(evs) != 1 || evs[0].Origin != "c1" {
		t.Fatalf("%+v %v", evs, err)
	}
}

func TestOutputThenQueuesFollowUpWork(t *testing.T) {
	g := newRig(t, nil)
	g.start(t, "c1")
	var got atomic.Value
	g.agent.handle = func(ctx context.Context, in AgentInput, e domain.Event) (AgentOutput, error) {
		return AgentOutput{Content: json.RawMessage(`{"v":"first"}`), Then: &Directive{Kind: "prefetch"}}, nil
	}
	g.agent.update = func(ctx context.Context, in AgentInput, d Directive) (AgentOutput, error) {
		got.Store(d.Kind)
		return AgentOutput{}, nil
	}
	g.r.Dispatch("c1", ev("1"))
	waitFor(t, "follow-up", func() bool { return got.Load() == "prefetch" })
	if g.sink.lastContent("c1") != `{"v":"first"}` {
		t.Fatal(g.sink.lastContent("c1"))
	}
}

func TestAccountProblemTellsTheStudentWhatToCheck(t *testing.T) {
	if msg := userMessage(fmt.Errorf("x: %w", provider.ErrAccount)); !strings.Contains(msg, "key") {
		t.Fatal(msg)
	}
}

func TestNoRouteExplainsThePrivacySetting(t *testing.T) {
	if msg := userMessage(fmt.Errorf("x: %w", provider.ErrNoRoute)); !strings.Contains(msg, "privacy") {
		t.Fatal(msg)
	}
}

// Slower models such as Qwen Flash on OpenRouter can take over 90 seconds to
// produce a game or quiz, so the default leaves them room.
func TestDefaultTaskTimeoutAllowsSlowModels(t *testing.T) {
	r := NewRuntime(RuntimeConfig{}, nil, nil, nil, nil)
	if got := r.cfg.TaskTimeout; got != 3*time.Minute {
		t.Fatalf("default task timeout = %v, want 3m", got)
	}
}
