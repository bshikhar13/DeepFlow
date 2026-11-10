package evaluator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"deepflow/internal/domain"
	"deepflow/internal/harness"
	"deepflow/internal/provider"
	"deepflow/internal/store"
)

type rig struct {
	e       *Evaluator
	mock    *provider.Mock
	bus     *harness.Bus
	ctx     *harness.ContextStore
	store   *store.Store
	sess    domain.Session
	mu      sync.Mutex
	results []Output
	stop    func()
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p, _ := st.CreatePursuit("Database Systems", "", 8)
	sess, _ := st.CreateSession(p.ID, "")
	g := &rig{mock: provider.NewMock(), store: st, sess: sess, bus: harness.NewBus(st), ctx: harness.NewContextStore(harness.LearningContext{PursuitTitle: p.Title})}
	g.e = New(Config{SessionID: sess.ID, PursuitID: p.ID, Coalesce: 20 * time.Millisecond, Provider: g.mock, Store: st, Context: g.ctx, Bus: g.bus,
		OnResult: func(o Output) {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.results = append(g.results, o)
		}})
	g.stop = g.e.Start()
	t.Cleanup(g.stop)
	return g
}

func (g *rig) pub(t *testing.T, ty domain.EventType, text string) {
	t.Helper()
	err := g.bus.Publish(domain.Event{SessionID: g.sess.ID, Type: ty, Payload: mustJSON(map[string]string{"text": text})})
	if err != nil {
		t.Fatal(err)
	}
}

func (g *rig) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.results)
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
	t.Fatalf("timed out: %s", what)
}

func TestSliderDoesNotTrigger(t *testing.T) {
	g := newRig(t)
	g.pub(t, domain.EvGameStar, "50")
	time.Sleep(60 * time.Millisecond)
	if g.mock.Calls("evaluator.assess") != 0 {
		t.Fatal("slider triggered evaluation")
	}
}

func TestNonTriggerEventsNeverEvaluated(t *testing.T) {
	g := newRig(t)
	for _, ty := range []domain.EventType{domain.EvAgentMessage, domain.EvUserPin, domain.EvOrchestratorDecide, domain.EvContextUpdated, domain.EvQuizResult, domain.EvQuizAnswered, domain.EvQuizNext} {
		g.pub(t, ty, "x")
	}
	time.Sleep(60 * time.Millisecond)
	if g.mock.Calls("evaluator.assess") != 0 {
		t.Fatal("non-trigger evaluated")
	}
}

func TestCoalescesBurst(t *testing.T) {
	g := newRig(t)
	for i := 0; i < 3; i++ {
		g.pub(t, domain.EvUserMessage, "message")
	}
	waitFor(t, "result", func() bool { return g.count() == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := g.mock.Calls("evaluator.assess"); n != 1 {
		t.Fatalf("calls %d", n)
	}
}

func TestMalformedOutputDiscarded(t *testing.T) {
	g := newRig(t)
	g.mock.Override("evaluator.assess", "{not json")
	before := g.ctx.Snapshot().Version
	g.pub(t, domain.EvUserMessage, "hello")
	waitFor(t, "call", func() bool { return g.mock.Calls("evaluator.assess") == 1 })
	time.Sleep(30 * time.Millisecond)
	if g.ctx.Snapshot().Version != before || g.count() != 0 {
		t.Fatal("malformed output changed state")
	}
	g.pub(t, domain.EvUserMessage, "still working")
	waitFor(t, "second result", func() bool { return g.count() == 1 })
}

func TestProviderFailureDoesNotStopEvaluator(t *testing.T) {
	g := newRig(t)
	g.mock.Fail("evaluator.assess", errors.New("down"))
	g.pub(t, domain.EvUserMessage, "one")
	waitFor(t, "call", func() bool { return g.mock.Calls("evaluator.assess") == 1 })
	time.Sleep(10 * time.Millisecond)
	g.mock.Fail("evaluator.assess", nil)
	g.pub(t, domain.EvUserMessage, "two")
	waitFor(t, "result", func() bool { return g.count() == 1 })
}

func TestEvidenceRecordedWithProvenance(t *testing.T) {
	g := newRig(t)
	g.pub(t, domain.EvUserMessage, "A full scan can win because reading every page sequentially costs less than many random reads when selectivity is poor.")
	waitFor(t, "evidence", func() bool {
		ev, _ := g.store.ListEvidence(g.sess.PursuitID)
		return len(ev) == 1
	})
	ev, _ := g.store.ListEvidence(g.sess.PursuitID)
	e := ev[0]
	if e.Tier != domain.TierStrong || e.SourceEventID == "" || e.Correctness != "correct" || e.Reasoning != "strong" || e.Uncertainty == "" {
		t.Fatalf("%+v", e)
	}
	src, err := g.store.GetEvent(e.SourceEventID)
	if err != nil || src.Type != domain.EvUserMessage {
		t.Fatalf("%+v %v", src, err)
	}
}

func TestExposureProducesNoEvidence(t *testing.T) {
	g := newRig(t)
	g.mock.Override("evaluator.assess", `{"intent":{"summary":"s","kind":"foundation","shifted":false},"concepts":[],"observations":[],"inferences":[],"evidence":[{"concept":"x","tier":"strong","correctness":"correct","reasoning":"strong","rationale":"r","uncertainty":"u","source_event_id":""}],"gaps":[],"open_questions":[],"crystallize":[],"readiness":"explain","recommendations":[]}`)
	g.pub(t, domain.EvConceptExplored, "viewed a definition")
	waitFor(t, "result", func() bool { return g.count() == 1 })
	ev, _ := g.store.ListEvidence(g.sess.PursuitID)
	if len(ev) != 0 {
		t.Fatalf("%+v", ev)
	}
}

func objectsResp(authorship string) string {
	return `{"intent":{"summary":"s","kind":"foundation","shifted":false},"concepts":[],"observations":[],"inferences":[],"evidence":[],"gaps":[],"open_questions":[],"crystallize":[{"type":"insight","content":"An insight.","authorship":"` + authorship + `"}],"readiness":"explain","recommendations":[]}`
}

func TestCrystallizePreservesAuthorship(t *testing.T) {
	g := newRig(t)
	g.mock.Override("evaluator.assess", objectsResp("ai"))
	g.pub(t, domain.EvUserMessage, "hi")
	waitFor(t, "result", func() bool { return g.count() == 1 })
	objs, _ := g.store.ListObjects(g.sess.PursuitID)
	if len(objs) != 1 || objs[0].Authorship != domain.AuthorAI {
		t.Fatalf("%+v", objs)
	}
}

func TestLearnerAuthorshipRequiresLearnerSource(t *testing.T) {
	g := newRig(t)
	g.mock.Override("evaluator.assess", objectsResp("learner"))
	g.pub(t, domain.EvConceptExplored, "viewed")
	waitFor(t, "result", func() bool { return g.count() == 1 })
	objs, _ := g.store.ListObjects(g.sess.PursuitID)
	if len(objs) != 1 || objs[0].Authorship != domain.AuthorAI {
		t.Fatalf("exposure credited to learner: %+v", objs)
	}
}

func TestInsightWithoutLearnerEvidenceStaysLowConfidence(t *testing.T) {
	g := newRig(t)
	g.mock.Override("evaluator.assess", objectsResp("learner"))
	g.pub(t, domain.EvUserMessage, "hi")
	waitFor(t, "result", func() bool { return g.count() == 1 })
	objs, _ := g.store.ListObjects(g.sess.PursuitID)
	if objs[0].Confidence != "low" {
		t.Fatalf("%+v", objs[0])
	}
}

func TestCrystallizeDedupesByContent(t *testing.T) {
	g := newRig(t)
	g.mock.Override("evaluator.assess", objectsResp("ai"), objectsResp("ai"))
	g.pub(t, domain.EvUserMessage, "one")
	waitFor(t, "first", func() bool { return g.count() == 1 })
	g.pub(t, domain.EvUserMessage, "two")
	waitFor(t, "second", func() bool { return g.count() == 2 })
	objs, _ := g.store.ListObjects(g.sess.PursuitID)
	if len(objs) != 1 {
		t.Fatalf("%+v", objs)
	}
}

func TestContextVersionBumpsAndEmitsContextUpdated(t *testing.T) {
	g := newRig(t)
	before := g.ctx.Snapshot().Version
	g.pub(t, domain.EvUserMessage, "Indexes always make searches faster")
	waitFor(t, "result", func() bool { return g.count() == 1 })
	c := g.ctx.Snapshot()
	if c.Version <= before || len(c.Gaps) == 0 || len(c.Hypotheses) == 0 || len(c.Misconceptions) == 0 {
		t.Fatalf("%+v", c)
	}
	evs, _ := g.store.ListEvents(g.sess.ID, domain.EvContextUpdated)
	if len(evs) != 1 || evs[0].ContextVersion != c.Version {
		t.Fatalf("%+v", evs)
	}
}

func TestInvalidRecommendationDropped(t *testing.T) {
	g := newRig(t)
	g.mock.Override("evaluator.assess", `{"intent":{"summary":"s","kind":"foundation","shifted":false},"concepts":[],"observations":[],"inferences":[],"evidence":[],"gaps":[],"open_questions":[],"crystallize":[],"readiness":"explain","recommendations":[{"action":"DELETE_EVERYTHING","cell_id":"","target_type":"","cell_type":"","objective":"","reason":"","priority":"high"},{"action":"UPDATE","cell_id":"","target_type":"quiz","cell_type":"quiz","objective":"o","reason":"r","priority":"low"},{"action":"SPAWN","cell_id":"","target_type":"","cell_type":"hologram","objective":"o","reason":"r","priority":"low"}]}`)
	g.pub(t, domain.EvUserMessage, "hi")
	waitFor(t, "result", func() bool { return g.count() == 1 })
	g.mu.Lock()
	defer g.mu.Unlock()
	if r := g.results[0].Recommendations; len(r) != 1 || r[0].Action != domain.ActUpdate {
		t.Fatalf("%+v", r)
	}
}

func TestNoWritesAfterStop(t *testing.T) {
	g := newRig(t)
	g.mock.Delay("evaluator.assess", 30*time.Millisecond)
	g.pub(t, domain.EvUserMessage, "A substantive explanation because of total access cost and selectivity in the plan.")
	waitFor(t, "call started", func() bool { return g.mock.Calls("evaluator.assess") == 1 })
	g.stop()
	time.Sleep(60 * time.Millisecond)
	ev, _ := g.store.ListEvidence(g.sess.PursuitID)
	if len(ev) != 0 || g.count() != 0 {
		t.Fatalf("wrote after stop: %+v", ev)
	}
}

// flaky fails its first call with err, then behaves like the Mock.
type flaky struct {
	*provider.Mock
	mu     sync.Mutex
	err    error
	limits []int
}

func (f *flaky) Structured(ctx context.Context, req provider.StructuredRequest, out any) (provider.Usage, error) {
	f.mu.Lock()
	f.limits = append(f.limits, req.MaxTokens)
	err := f.err
	f.err = nil
	f.mu.Unlock()
	if err != nil {
		return provider.Usage{}, err
	}
	return f.Mock.Structured(ctx, req, out)
}

func TestTruncatedEvaluationRetriesWithMoreRoom(t *testing.T) {
	g := newRig(t)
	g.stop()
	f := &flaky{Mock: provider.NewMock(), err: fmt.Errorf("%w: cut off", provider.ErrTruncated)}
	e := New(Config{SessionID: g.sess.ID, PursuitID: g.sess.PursuitID, Coalesce: 5 * time.Millisecond, Provider: f, Store: g.store, Context: g.ctx, Bus: g.bus,
		OnResult: func(o Output) {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.results = append(g.results, o)
		}})
	stop := e.Start()
	defer stop()
	g.pub(t, domain.EvUserMessage, "give me a game")
	waitFor(t, "recovered", func() bool { return g.count() == 1 })
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.limits) != 2 || f.limits[1] <= f.limits[0] {
		t.Fatalf("limits %v", f.limits)
	}
}

func TestEvaluatorSchemaAcceptsRepeatedFailureQuietly(t *testing.T) {
	g := newRig(t)
	g.mock.Fail("evaluator.assess", fmt.Errorf("%w: cut off", provider.ErrTruncated))
	g.pub(t, domain.EvUserMessage, "one")
	waitFor(t, "both attempts", func() bool { return g.mock.Calls("evaluator.assess") == 2 })
	time.Sleep(20 * time.Millisecond)
	if g.count() != 0 {
		t.Fatal("no result expected")
	}
}
