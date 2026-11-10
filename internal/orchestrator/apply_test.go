package orchestrator

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"deepflow/internal/agents"
	"deepflow/internal/domain"
	"deepflow/internal/evaluator"
	"deepflow/internal/harness"
	"deepflow/internal/provider"
	"deepflow/internal/store"
)

type sink struct {
	mu sync.Mutex
	o  *Orchestrator
}

func (s *sink) CellUpdated(c domain.Cell)                  { s.o.MergeCell(c) }
func (s *sink) CellStatus(id string, st domain.CellStatus) { s.o.SetStatus(id, st) }
func (s *sink) Delta(id, text string)                      {}
func (s *sink) Request(id string, r harness.CellRequest)   { s.o.Request(id, r) }

type notes struct {
	mu        sync.Mutex
	decisions []domain.Decision
}

func (n *notes) WorkspaceChanged(domain.WorkspaceState) {}
func (n *notes) CellChanged(domain.Cell)                {}
func (n *notes) DecisionMade(d domain.Decision, canUndo bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.decisions = append(n.decisions, d)
}

type rig struct {
	o     *Orchestrator
	rt    *harness.Runtime
	store *store.Store
	sess  domain.Session
	now   time.Time
	mu    sync.Mutex
	notes *notes
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p, _ := st.CreatePursuit("Force and Motion", "", 7)
	sess, _ := st.CreateSession(p.ID, "Why?")
	bus := harness.NewBus(st)
	cs := harness.NewContextStore(harness.LearningContext{PursuitTitle: p.Title})
	sk := &sink{}
	rt := harness.NewRuntime(harness.RuntimeConfig{SessionID: sess.ID, Provider: provider.NewMock()}, bus, cs, agents.New, sk)
	t.Cleanup(rt.Stop)
	g := &rig{rt: rt, store: st, sess: sess, now: t0, notes: &notes{}}
	g.o = New(Config{SessionID: sess.ID, Inquiry: "Why?", Policy: DefaultPolicy(), Engine: rt, Store: st, Bus: bus, Context: cs, Notifier: g.notes,
		Now: func() time.Time { g.mu.Lock(); defer g.mu.Unlock(); return g.now }})
	sk.o = g.o
	g.o.Init(InitialCells("Why?"))
	waitFor(t, "cells ready", func() bool {
		for _, c := range g.o.Snapshot().Cells {
			if c.Status != domain.StatusWaiting {
				return false
			}
		}
		return true
	})
	return g
}

func (g *rig) advance(d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.now = g.now.Add(d)
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

func (g *rig) byType(ct domain.CellType) *domain.Cell {
	for _, c := range g.o.Snapshot().Cells {
		if c.Type == ct {
			cc := c
			return &cc
		}
	}
	return nil
}

func TestUserRemoveBelowMinSpawnsReplacementImmediately(t *testing.T) {
	g := newRig(t)
	con := g.byType(domain.CellConcept)
	if err := g.o.UserRemove(con.ID); err != nil {
		t.Fatal(err)
	}
	cells := g.o.Snapshot().Cells
	if len(cells) != 6 {
		t.Fatalf("after removal %d", len(cells))
	}
	for _, c := range cells {
		if c.ID == con.ID {
			t.Fatal("removed cell came back")
		}
	}
	if g.byType(domain.CellConcept) != nil {
		t.Fatal("replacement reused a type the learner removed")
	}
	if g.byType(domain.CellThought) == nil {
		t.Fatal("expected a What If card")
	}
}

func TestUserCannotRemoveDialogue(t *testing.T) {
	g := newRig(t)
	if err := g.o.UserRemove(g.byType(domain.CellDialogue).ID); !errors.Is(err, ErrProtected) {
		t.Fatalf("got %v", err)
	}
}

func TestUserPinOverridesPendingReplace(t *testing.T) {
	g := newRig(t)
	con := g.byType(domain.CellConcept)
	g.o.Handle([]evaluator.Recommendation{{Action: domain.ActReplace, CellID: con.ID, CellType: domain.CellGame, Objective: "first"}})
	waitFor(t, "first applied", func() bool { return g.byType(domain.CellConcept) == nil })
	tho := g.byType(domain.CellQuiz)
	g.o.Handle([]evaluator.Recommendation{{Action: domain.ActReplace, CellID: tho.ID, CellType: domain.CellThought, Objective: "later"}})
	if len(g.o.Snapshot().Pending) != 1 {
		t.Fatalf("pending %+v", g.o.Snapshot().Pending)
	}
	g.o.UserPin(tho.ID)
	g.advance(10 * time.Minute)
	g.o.Tick()
	if g.byType(domain.CellQuiz) == nil {
		t.Fatal("pinned cell was replaced")
	}
	if len(g.o.Snapshot().Pending) != 0 {
		t.Fatal("pending should be cleared")
	}
}

func TestBusyLeaseExpires(t *testing.T) {
	g := newRig(t)
	con := g.byType(domain.CellConcept)
	g.o.SetBusy(con.ID, 20*time.Second)
	g.o.Handle([]evaluator.Recommendation{{Action: domain.ActReplace, CellID: con.ID, CellType: domain.CellGame, Objective: "x"}})
	if g.byType(domain.CellConcept) == nil {
		t.Fatal("busy cell replaced")
	}
	g.advance(10 * time.Second)
	g.o.Tick()
	if g.byType(domain.CellConcept) == nil {
		t.Fatal("replaced before lease expired")
	}
	g.advance(11 * time.Second)
	g.o.Tick()
	if g.byType(domain.CellConcept) != nil {
		t.Fatal("pending change not applied after lease expiry")
	}
}

func TestReplaceRetainsArtifactsAndTerminatesAgent(t *testing.T) {
	g := newRig(t)
	con := g.byType(domain.CellConcept)
	g.o.Handle([]evaluator.Recommendation{{Action: domain.ActReplace, CellID: con.ID, CellType: domain.CellGame, Objective: "Compare plans", Reason: "moved on"}})
	waitFor(t, "artifact", func() bool {
		a, _ := g.store.ListArtifacts(g.sess.ID)
		return len(a) == 1
	})
	a, _ := g.store.ListArtifacts(g.sess.ID)
	if a[0].CellID != con.ID || a[0].CellType != domain.CellConcept || !strings.Contains(string(a[0].Content), "Force") {
		t.Fatalf("%+v", a[0])
	}
	cells := g.o.Snapshot().Cells
	if len(cells) != 6 {
		t.Fatalf("%d cells", len(cells))
	}
	g.rt.Dispatch(con.ID, domain.Event{Type: domain.EvConceptExplored, Payload: json.RawMessage(`{"concept":"x"}`)}) // no-op, no panic
}

func TestReplaceKeepsOtherCellsStable(t *testing.T) {
	g := newRig(t)
	before := g.o.Snapshot().Cells
	con := g.byType(domain.CellConcept)
	g.o.Handle([]evaluator.Recommendation{{Action: domain.ActReplace, CellID: con.ID, CellType: domain.CellGame, Objective: "o"}})
	after := g.o.Snapshot().Cells
	for i := range before {
		if before[i].ID == con.ID {
			if after[i].Layout.Col != before[i].Layout.Col || after[i].Layout.Row != before[i].Layout.Row {
				t.Fatal("replacement should take the old cell's position")
			}
			continue
		}
		if after[i].ID != before[i].ID || after[i].Layout != before[i].Layout {
			t.Fatalf("cell %d moved: %+v -> %+v", i, before[i].Layout, after[i].Layout)
		}
	}
}

func TestUndoRestoresPriorCellAndLayout(t *testing.T) {
	g := newRig(t)
	before := g.o.Snapshot().Cells
	con := g.byType(domain.CellConcept)
	g.o.Handle([]evaluator.Recommendation{{Action: domain.ActReplace, CellID: con.ID, CellType: domain.CellGame, Objective: "o"}})
	if err := g.o.Undo(); err != nil {
		t.Fatal(err)
	}
	after := g.o.Snapshot().Cells
	if len(after) != len(before) {
		t.Fatalf("%d vs %d", len(after), len(before))
	}
	for i := range before {
		if after[i].ID != before[i].ID || after[i].Layout != before[i].Layout {
			t.Fatalf("%d: %+v vs %+v", i, after[i], before[i])
		}
	}
	restored := g.byType(domain.CellConcept)
	if string(restored.Content) != string(con.Content) {
		t.Fatalf("content lost: %s", restored.Content)
	}
	if restored.AgentInstanceID == con.AgentInstanceID {
		t.Fatal("restored cell needs a new agent instance")
	}
	if err := g.o.Undo(); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("got %v", err)
	}
}

func TestUndoAfterSecondChangeRevertsLatestOnly(t *testing.T) {
	g := newRig(t)
	con := g.byType(domain.CellConcept)
	g.o.Handle([]evaluator.Recommendation{{Action: domain.ActReplace, CellID: con.ID, CellType: domain.CellGame, Objective: "first"}})
	g.advance(10 * time.Minute)
	tho := g.byType(domain.CellQuiz)
	g.o.Handle([]evaluator.Recommendation{{Action: domain.ActReplace, CellID: tho.ID, CellType: domain.CellThought, Objective: "second"}})
	if g.byType(domain.CellQuiz) != nil {
		t.Fatal("second replacement should have applied")
	}
	if err := g.o.Undo(); err != nil {
		t.Fatal(err)
	}
	if g.byType(domain.CellQuiz) == nil {
		t.Fatal("latest not reverted")
	}
	if g.byType(domain.CellConcept) != nil {
		t.Fatal("earlier change should stay")
	}
}

func TestDecisionPersistedWithReason(t *testing.T) {
	g := newRig(t)
	con := g.byType(domain.CellConcept)
	g.o.Handle([]evaluator.Recommendation{{Action: domain.ActReplace, CellID: con.ID, CellType: domain.CellGame, Objective: "o", Reason: "learner moved on"}})
	evs, err := g.store.ListEvents(g.sess.ID, domain.EvOrchestratorDecide)
	if err != nil || len(evs) == 0 {
		t.Fatalf("%v %v", evs, err)
	}
	if !strings.Contains(string(evs[len(evs)-1].Payload), "learner moved on") {
		t.Fatalf("%s", evs[len(evs)-1].Payload)
	}
	g.notes.mu.Lock()
	defer g.notes.mu.Unlock()
	if len(g.notes.decisions) == 0 {
		t.Fatal("no notification")
	}
}

func TestSnapshotSavedAfterStructuralChange(t *testing.T) {
	g := newRig(t)
	g.o.Handle([]evaluator.Recommendation{{Action: domain.ActSpawn, CellType: domain.CellThought, Objective: "extra"}})
	s, _ := g.store.GetSession(g.sess.ID)
	var ws domain.WorkspaceState
	if err := json.Unmarshal(s.Snapshot, &ws); err != nil || len(ws.Cells) != 7 {
		t.Fatalf("%v %d", err, len(ws.Cells))
	}
}

func TestUserRefreshDirectsAgent(t *testing.T) {
	g := newRig(t)
	pra := g.byType(domain.CellQuiz)
	g.o.UserRefresh(pra.ID)
	// The mock returns the same challenge; refresh must complete without failing.
	waitFor(t, "refreshed", func() bool { return g.byType(domain.CellQuiz).Status == domain.StatusWaiting })
}

func TestReplaceKeepsDraftAsLearnerArtifact(t *testing.T) {
	g := newRig(t)
	con := g.byType(domain.CellConcept)
	if err := g.store.SaveDraft(g.sess.ID, con.ID, "my unfinished thinking"); err != nil {
		t.Fatal(err)
	}
	g.o.Handle([]evaluator.Recommendation{{Action: domain.ActReplace, CellID: con.ID, CellType: domain.CellGame, Objective: "o"}})
	arts, _ := g.store.ListArtifacts(g.sess.ID)
	found := false
	for _, a := range arts {
		found = found || (a.LearnerAuthored && strings.Contains(string(a.Content), "my unfinished thinking"))
	}
	if !found {
		t.Fatalf("draft not preserved: %+v", arts)
	}
}

func TestUndoRestoresDraftOfRemovedCell(t *testing.T) {
	g := newRig(t)
	pra := g.byType(domain.CellQuiz)
	if err := g.store.SaveDraft(g.sess.ID, pra.ID, "half an answer"); err != nil {
		t.Fatal(err)
	}
	if err := g.o.UserRemove(pra.ID); err != nil {
		t.Fatal(err)
	}
	if d, _ := g.store.GetDrafts(g.sess.ID); d[pra.ID] != "" {
		t.Fatalf("draft should move to an artifact on removal: %v", d)
	}
	if err := g.o.Undo(); err != nil {
		t.Fatal(err)
	}
	if d, _ := g.store.GetDrafts(g.sess.ID); d[pra.ID] != "half an answer" {
		t.Fatalf("draft not restored: %v", d)
	}
}

func TestUndoOfSpawnKeepsItsDraft(t *testing.T) {
	g := newRig(t)
	g.o.Handle([]evaluator.Recommendation{{Action: domain.ActSpawn, CellType: domain.CellQuiz, Objective: "a different objective"}})
	var spawned string
	for _, c := range g.o.Snapshot().Cells {
		if c.Objective == "a different objective" {
			spawned = c.ID
		}
	}
	if err := g.store.SaveDraft(g.sess.ID, spawned, "typed into the new cell"); err != nil {
		t.Fatal(err)
	}
	if err := g.o.Undo(); err != nil {
		t.Fatal(err)
	}
	arts, _ := g.store.ListArtifacts(g.sess.ID)
	found := false
	for _, a := range arts {
		found = found || (a.LearnerAuthored && strings.Contains(string(a.Content), "typed into the new cell"))
	}
	if !found {
		t.Fatalf("draft lost: %+v", arts)
	}
}
