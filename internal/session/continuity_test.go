package session

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deepflow/internal/agents"
	"deepflow/internal/domain"
	"deepflow/internal/evaluator"
	"deepflow/internal/orchestrator"
	"deepflow/internal/provider"
	"deepflow/internal/store"
)

type rig struct {
	st   *store.Store
	mock *provider.Mock
	m    *Manager
	pid  string
}

func orchestratorCells() []domain.Cell { return orchestrator.InitialCells("Why?") }

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	mock := provider.NewMock()
	m := NewManager(Config{Store: st, Provider: mock, Coalesce: 5 * time.Millisecond, Policy: orchestrator.DefaultPolicy(), TickEvery: 10 * time.Millisecond})
	t.Cleanup(func() { m.Shutdown(); st.Close() })
	p, _ := st.CreatePursuit("Force and Motion", "", 7)
	return &rig{st: st, mock: mock, m: m, pid: p.ID}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	dl := time.Now().Add(5 * time.Second)
	for time.Now().Before(dl) {
		if cond() {
			return
		}
		time.Sleep(3 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

func ready(t *testing.T, l *Live) {
	t.Helper()
	waitFor(t, "cells ready", func() bool {
		for _, c := range l.Workspace().Cells {
			if len(c.Content) == 0 || c.Status != domain.StatusWaiting {
				return false
			}
		}
		return true
	})
}

func cell(l *Live, ct domain.CellType) domain.Cell {
	for _, c := range l.Workspace().Cells {
		if c.Type == ct {
			return c
		}
	}
	return domain.Cell{}
}

func say(t *testing.T, l *Live, text string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"text": text})
	if err := l.Action(cell(l, domain.CellDialogue).ID, domain.EvUserMessage, b, domain.NewID("c")); err != nil {
		t.Fatal(err)
	}
}

func endAndReturn(t *testing.T, g *rig) (*Live, domain.Session) {
	t.Helper()
	l, err := g.m.Start(g.pid, "Why?")
	if err != nil {
		t.Fatal(err)
	}
	ready(t, l)
	s, err := g.m.End(l.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	l2, err := g.m.Start(g.pid, "Next")
	if err != nil {
		t.Fatal(err)
	}
	ready(t, l2)
	return l2, s
}

func TestFirstSessionNoRetrieval(t *testing.T) {
	g := newRig(t)
	l, err := g.m.Start(g.pid, "Why?")
	if err != nil {
		t.Fatal(err)
	}
	ready(t, l)
	time.Sleep(20 * time.Millisecond)
	if q, _ := g.m.RetrievalQuestions(l.SessionID()); len(q) != 0 {
		t.Fatalf("%+v", q)
	}
}

func TestEndConsolidatesWithProvenance(t *testing.T) {
	g := newRig(t)
	g.mock.Override("session.consolidate", `{"summary":"s","insights":["An insight."],"gaps":["A gap."],"open_questions":["A question?"]}`)
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	say(t, l, "Hello there")
	waitFor(t, "reply", func() bool { return g.mock.Calls("evaluator.assess") > 0 })
	s, err := g.m.End(l.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if s.Summary != "s" || !s.Ended() || s.NeedsConsolidation {
		t.Fatalf("%+v", s)
	}
	objs, _ := g.st.ListObjects(g.pid)
	byContent := map[string]domain.ConceptObject{}
	for _, o := range objs {
		byContent[o.Content] = o
	}
	in, ok := byContent["An insight."]
	if !ok || in.Authorship != domain.AuthorAI || in.Confidence != "low" || in.SourceEventID == "" {
		t.Fatalf("%+v", in)
	}
	if q, ok := byContent["A question?"]; !ok || q.Type != domain.ObjQuestion {
		t.Fatalf("%+v", q)
	}
	if _, live := g.m.Get(l.SessionID()); live {
		t.Fatal("session should no longer be live")
	}
}

func TestEndSucceedsWhenModelFails(t *testing.T) {
	g := newRig(t)
	g.mock.Fail("session.consolidate", errors.New("down"))
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	s, err := g.m.End(l.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if !s.Ended() || !s.NeedsConsolidation {
		t.Fatalf("%+v", s)
	}
	g.mock.Fail("session.consolidate", nil)
	l2, err := g.m.Start(g.pid, "Next")
	if err != nil {
		t.Fatal(err)
	}
	ready(t, l2)
	waitFor(t, "late consolidation", func() bool {
		x, _ := g.st.GetSession(s.ID)
		return !x.NeedsConsolidation && x.Summary != ""
	})
}

func TestReturnSessionOffersUpToThreeRetrievalQuestions(t *testing.T) {
	g := newRig(t)
	g.mock.Override("session.retrieval", `{"questions":[{"prompt":"a","concept":"x"},{"prompt":"b","concept":"x"},{"prompt":"c","concept":"x"},{"prompt":"d","concept":"x"}]}`)
	l2, _ := endAndReturn(t, g)
	waitFor(t, "questions", func() bool {
		q, _ := g.m.RetrievalQuestions(l2.SessionID())
		return len(q) > 0
	})
	q, _ := g.m.RetrievalQuestions(l2.SessionID())
	if len(q) != 3 || q[0].Status != "open" || q[0].ID == "" {
		t.Fatalf("%+v", q)
	}
}

func TestResumeOpenSessionHasNoRetrieval(t *testing.T) {
	g := newRig(t)
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	before := g.mock.Calls("session.retrieval")
	l2, err := g.m.Start(g.pid, "ignored")
	if err != nil {
		t.Fatal(err)
	}
	if l2.SessionID() != l.SessionID() {
		t.Fatal("should resume the open session")
	}
	time.Sleep(20 * time.Millisecond)
	if g.mock.Calls("session.retrieval") != before {
		t.Fatal("no retrieval when resuming")
	}
}

func answer(t *testing.T, g *rig, l *Live, text string, skip bool) string {
	t.Helper()
	waitFor(t, "questions", func() bool {
		q, _ := g.m.RetrievalQuestions(l.SessionID())
		return len(q) > 0
	})
	q, _ := g.m.RetrievalQuestions(l.SessionID())
	if err := g.m.AnswerRetrieval(l.SessionID(), q[0].ID, text, skip); err != nil {
		t.Fatal(err)
	}
	return q[0].ID
}

func TestRetrievalAnswerCanProduceDurable(t *testing.T) {
	g := newRig(t)
	l2, _ := endAndReturn(t, g)
	answer(t, g, l2, "Selectivity decides whether an index beats a scan, since each match costs a random read.", false)
	waitFor(t, "durable evidence", func() bool {
		ev, _ := g.st.ListEvidence(g.pid)
		for _, e := range ev {
			if e.Tier == domain.TierDurable {
				return true
			}
		}
		return false
	})
}

func TestRetrievalSkipRecordsNoEvidence(t *testing.T) {
	g := newRig(t)
	l2, _ := endAndReturn(t, g)
	id := answer(t, g, l2, "", true)
	time.Sleep(40 * time.Millisecond)
	ev, _ := g.st.ListEvidence(g.pid)
	if len(ev) != 0 {
		t.Fatalf("%+v", ev)
	}
	q, _ := g.m.RetrievalQuestions(l2.SessionID())
	for _, x := range q {
		if x.ID == id && x.Status != "skipped" {
			t.Fatalf("%+v", x)
		}
	}
}

func TestRetrievalInformsInitialWorkspace(t *testing.T) {
	g := newRig(t)
	l2, _ := endAndReturn(t, g)
	answer(t, g, l2, "I don't know", false)
	waitFor(t, "concept refocused", func() bool {
		return strings.Contains(cell(l2, domain.CellConcept).Objective, "foundations")
	})
}

func TestDocumentPersistsAcrossSessions(t *testing.T) {
	g := newRig(t)
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	say(t, l, "Indexes always make searches faster")
	waitFor(t, "object", func() bool {
		o, _ := g.st.ListObjects(g.pid)
		return len(o) > 0
	})
	g.m.End(l.SessionID())
	l2, err := g.m.Start(g.pid, "Next")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(l2.ContextObjects()); n == 0 {
		t.Fatal("new session should see the Living Document")
	}
}

func TestExportIncludesAllEntities(t *testing.T) {
	g := newRig(t)
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	say(t, l, "A heavier cart speeds up less because the same push is shared across more mass.")
	waitFor(t, "evidence", func() bool {
		e, _ := g.st.ListEvidence(g.pid)
		return len(e) > 0
	})
	b, err := g.m.Export(g.pid)
	if err != nil {
		t.Fatal(err)
	}
	if b.Pursuit.ID != g.pid || len(b.Sessions) != 1 || len(b.Sessions[0].Messages) == 0 || len(b.Objects) == 0 || len(b.Evidence) == 0 {
		t.Fatalf("%+v", b)
	}
}

func TestDeleteLiveSessionStopsRuntime(t *testing.T) {
	g := newRig(t)
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	sid := l.SessionID()
	g.mock.Delay("game.spec", 50*time.Millisecond)
	if err := l.Action(cell(l, domain.CellGame).ID, domain.EvGameNext, nil, "x"); err != nil {
		t.Fatal(err)
	}
	if err := g.m.Delete(g.pid); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, live := g.m.Get(sid); live {
		t.Fatal("still live")
	}
	if _, err := g.st.GetSession(sid); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("session row: %v", err)
	}
	if e, _ := g.st.ListEvidence(g.pid); len(e) != 0 {
		t.Fatal("late write recreated rows")
	}
}

func quizChoice(t *testing.T, l *Live, right bool) {
	t.Helper()
	c := cell(l, domain.CellQuiz)
	var view agents.QuizContent
	if err := json.Unmarshal(c.Content, &view); err != nil {
		t.Fatal(err)
	}
	var st struct {
		Current struct {
			Answer int `json:"answer"`
		} `json:"current"`
	}
	if err := json.Unmarshal(c.LocalState, &st); err != nil {
		t.Fatal(err)
	}
	choice := st.Current.Answer
	if !right {
		choice = (choice + 1) % len(view.Options)
	}
	b, _ := json.Marshal(map[string]any{"questionId": view.ID, "choice": choice})
	if err := l.Action(c.ID, domain.EvQuizAnswered, b, domain.NewID("c")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "result", func() bool {
		var v agents.QuizContent
		json.Unmarshal(cell(l, domain.CellQuiz).Content, &v)
		return v.Result != nil
	})
	if err := l.Action(c.ID, domain.EvQuizNext, nil, domain.NewID("c")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "next question", func() bool {
		var v agents.QuizContent
		json.Unmarshal(cell(l, domain.CellQuiz).Content, &v)
		return v.Result == nil && v.ID != view.ID
	})
}

func journeyOf(t *testing.T, g *rig) domain.Journey {
	t.Helper()
	p, err := g.st.GetPursuit(g.pid)
	if err != nil || p.Journey == nil {
		t.Fatalf("%+v %v", p.Journey, err)
	}
	return *p.Journey
}

func TestJourneyIsSavedAndKeptAcrossSessions(t *testing.T) {
	g := newRig(t)
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	j := journeyOf(t, g)
	if len(j.Topics) < 2 || j.Current != 0 {
		t.Fatalf("%+v", j)
	}
	calls := g.mock.Calls("journey.index")
	g.m.End(l.SessionID())
	l2, _ := g.m.Start(g.pid, "Next")
	ready(t, l2)
	if g.mock.Calls("journey.index") != calls {
		t.Fatal("index regenerated")
	}
}

func TestCorrectAnswersAdvanceTheJourney(t *testing.T) {
	g := newRig(t)
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	quizChoice(t, l, true)
	waitFor(t, "step", func() bool { return journeyOf(t, g).Topics[0].Progress == 1 })
	quizChoice(t, l, false)
	time.Sleep(20 * time.Millisecond)
	if p := journeyOf(t, g).Topics[0].Progress; p != 1 {
		t.Fatalf("a wrong answer changed progress to %d", p)
	}
	quizChoice(t, l, true)
	quizChoice(t, l, true)
	waitFor(t, "milestone", func() bool { return journeyOf(t, g).Current == 1 })
	if !journeyOf(t, g).Topics[0].Done {
		t.Fatal("topic should be done")
	}
	waitFor(t, "cards follow the new topic", func() bool {
		return strings.Contains(cell(l, domain.CellQuiz).Objective, journeyOf(t, g).Topics[1].Title)
	})
	waitFor(t, "journey card updated", func() bool {
		var c agents.JourneyContent
		json.Unmarshal(cell(l, domain.CellJourney).Content, &c)
		return c.Current == 1
	})
}

func TestQuizAnswersBecomeBoundedEvidence(t *testing.T) {
	g := newRig(t)
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	quizChoice(t, l, true)
	quizChoice(t, l, false)
	waitFor(t, "evidence", func() bool {
		e, _ := g.st.ListEvidence(g.pid)
		return len(e) == 2
	})
	ev, _ := g.st.ListEvidence(g.pid)
	for _, e := range ev {
		if e.Tier != domain.TierModerate && e.Tier != domain.TierWeak {
			t.Fatalf("a multiple-choice answer can't be stronger than moderate: %+v", e)
		}
		src, err := g.st.GetEvent(e.SourceEventID)
		if err != nil || src.Type != domain.EvQuizAnswered {
			t.Fatalf("%+v %v", src, err)
		}
	}
}

func TestQuizBatchReachesEvaluatorOnlyEveryFiveAnswers(t *testing.T) {
	g := newRig(t)
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	before := g.mock.Calls("evaluator.assess")
	for i := 0; i < 4; i++ {
		quizChoice(t, l, true)
	}
	time.Sleep(30 * time.Millisecond)
	if n := g.mock.Calls("evaluator.assess"); n != before {
		t.Fatalf("evaluated per answer: %d", n-before)
	}
	quizChoice(t, l, false)
	waitFor(t, "batch evaluation", func() bool { return g.mock.Calls("evaluator.assess") == before+1 })
}

func TestGameStarIsAStepButNotEvidence(t *testing.T) {
	g := newRig(t)
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	b, _ := json.Marshal(map[string]any{"level": 0, "values": map[string]float64{"force": 50, "mass": 10}})
	if err := l.Action(cell(l, domain.CellGame).ID, domain.EvGameLevelWon, b, "w1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "step", func() bool { return journeyOf(t, g).Topics[0].Progress == 1 })
	if e, _ := g.st.ListEvidence(g.pid); len(e) != 0 {
		t.Fatalf("playing a game isn't evidence of understanding: %+v", e)
	}
}

func TestOldSnapshotWithRemovedCardsStartsFresh(t *testing.T) {
	g := newRig(t)
	s, err := g.st.CreateSession(g.pid, "Why?")
	if err != nil {
		t.Fatal(err)
	}
	old := domain.WorkspaceState{SessionID: s.ID, Cells: []domain.Cell{
		{ID: "a", Type: domain.CellDialogue, Content: json.RawMessage(`{}`)},
		{ID: "b", Type: "practice", Content: json.RawMessage(`{}`)},
	}}
	if err := g.st.SaveSnapshot(s.ID, old); err != nil {
		t.Fatal(err)
	}
	l, err := g.m.Start(g.pid, "Why?")
	if err != nil {
		t.Fatal(err)
	}
	ready(t, l)
	if l.SessionID() != s.ID || cell(l, domain.CellQuiz).ID == "" || len(l.Workspace().Cells) != 6 {
		t.Fatalf("%+v", l.Workspace().Cells)
	}
}

func TestStepsEarnedBeforeTheIndexExistsAreKept(t *testing.T) {
	g := newRig(t)
	g.mock.Delay("journey.index", 400*time.Millisecond)
	l, err := g.m.Start(g.pid, "Why?")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "game ready", func() bool { return len(cell(l, domain.CellGame).Content) > 0 })
	b, _ := json.Marshal(map[string]any{"level": 0, "values": map[string]float64{"force": 50, "mass": 10}})
	if err := l.Action(cell(l, domain.CellGame).ID, domain.EvGameLevelWon, b, "w1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "star", func() bool { return strings.Contains(string(cell(l, domain.CellGame).Content), `"stars":1`) })
	waitFor(t, "step credited once the index arrives", func() bool {
		p, _ := g.st.GetPursuit(g.pid)
		return p.Journey != nil && p.Journey.Topics[0].Progress == 1
	})
	waitFor(t, "journey card shows it", func() bool {
		var c agents.JourneyContent
		json.Unmarshal(cell(l, domain.CellJourney).Content, &c)
		return len(c.Topics) > 0 && c.Topics[0].Progress == 1
	})
}

func TestOldContentShapeRebuildsOnResume(t *testing.T) {
	g := newRig(t)
	s, err := g.st.CreateSession(g.pid, "Why?")
	if err != nil {
		t.Fatal(err)
	}
	cells := orchestratorCells()
	for i := range cells {
		switch cells[i].Type {
		case domain.CellConcept:
			cells[i].Content = json.RawMessage(`{"title":"Old","definition":"from before the redesign"}`)
		case domain.CellGame:
			cells[i].Content = json.RawMessage(`{"title":"Old chart","series":[]}`)
		case domain.CellQuiz:
			cells[i].Content = json.RawMessage(`{"prompt":"old"}`)
		case domain.CellJourney:
			cells[i].Content = json.RawMessage(`{}`)
		default:
			cells[i].Content = json.RawMessage(`{}`)
		}
	}
	if err := g.st.SaveSnapshot(s.ID, domain.WorkspaceState{SessionID: s.ID, Cells: cells}); err != nil {
		t.Fatal(err)
	}
	l, err := g.m.Start(g.pid, "Why?")
	if err != nil {
		t.Fatal(err)
	}
	ready(t, l)
	var concept agents.ConceptContent
	json.Unmarshal(cell(l, domain.CellConcept).Content, &concept)
	if concept.Idea == "" {
		t.Fatalf("concept not rebuilt: %s", cell(l, domain.CellConcept).Content)
	}
	var game agents.GameContent
	json.Unmarshal(cell(l, domain.CellGame).Content, &game)
	if game.Kind == "" || game.Won == nil {
		t.Fatalf("game not rebuilt: %s", cell(l, domain.CellGame).Content)
	}
	var quiz agents.QuizContent
	json.Unmarshal(cell(l, domain.CellQuiz).Content, &quiz)
	if len(quiz.Options) == 0 {
		t.Fatalf("quiz not rebuilt: %s", cell(l, domain.CellQuiz).Content)
	}
}

func TestDemonstratedHasOneQuizEntryPerTopic(t *testing.T) {
	g := newRig(t)
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	quizChoice(t, l, true)
	quizChoice(t, l, true)
	waitFor(t, "two steps", func() bool { return journeyOf(t, g).Topics[0].Progress == 2 })
	quizChoice(t, l, true)
	seen := map[string]bool{}
	for _, d := range l.ctx.Snapshot().Demonstrated {
		if seen[d.Text] {
			t.Fatalf("repeated entry in every prompt: %q", d.Text)
		}
		seen[d.Text] = true
	}
}

func gameID(l *Live) string {
	var c agents.GameContent
	json.Unmarshal(cell(l, domain.CellGame).Content, &c)
	return c.ID
}

func TestAskingForAGameInChatChangesTheGameCard(t *testing.T) {
	g := newRig(t)
	g.mock.Fail("evaluator.assess", errors.New("evaluator is down")) // must not depend on it
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	before := gameID(l)
	say(t, l, "give me a game")
	waitFor(t, "new game", func() bool { return gameID(l) != before })
	if !strings.Contains(cell(l, domain.CellGame).Objective, "give me a game") {
		t.Fatalf("the game should know what was asked: %q", cell(l, domain.CellGame).Objective)
	}
	found := false
	for _, d := range l.Decisions() {
		found = found || (d.Decision.CellType == domain.CellGame && !d.Decision.Unsolicited && d.Decision.Reason != "")
	}
	if !found {
		t.Fatalf("activity should explain it: %+v", l.Decisions())
	}
}

func TestAskingForAGameWorksEvenJustAfterAnUpdate(t *testing.T) {
	g := newRig(t)
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	first := gameID(l)
	say(t, l, "give me a game")
	waitFor(t, "first", func() bool { return gameID(l) != first })
	second := gameID(l)
	say(t, l, "another game please")
	waitFor(t, "second, inside the content cooldown", func() bool { return gameID(l) != second })
}

func TestAskingForARemovedCardBringsItBack(t *testing.T) {
	g := newRig(t)
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	if err := l.Remove(cell(l, domain.CellGame).ID); err != nil {
		t.Fatal(err)
	}
	if cell(l, domain.CellGame).ID != "" {
		t.Fatal("removed")
	}
	say(t, l, "give me a game")
	waitFor(t, "game back", func() bool { return cell(l, domain.CellGame).ID != "" && len(cell(l, domain.CellGame).Content) > 0 })
}

func TestEveryInteractionReachesSharedContext(t *testing.T) {
	g := newRig(t)
	g.mock.Fail("evaluator.assess", errors.New("evaluator is down")) // must not depend on it
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	recent := func(want string) bool {
		for _, r := range l.ctx.Snapshot().Recent {
			if strings.Contains(r.Text, want) {
				return true
			}
		}
		return false
	}
	say(t, l, "where are forces used")
	waitFor(t, "chat in context", func() bool { return recent("where are forces used") })
	quizChoice(t, l, true)
	waitFor(t, "quiz in context", func() bool { return recent("Right on") })
	quizChoice(t, l, false)
	waitFor(t, "wrong answer in context", func() bool { return recent("Wrong on") })
	b, _ := json.Marshal(map[string]any{"level": 0, "values": map[string]float64{"force": 50, "mass": 10}})
	if err := l.Action(cell(l, domain.CellGame).ID, domain.EvGameLevelWon, b, "w1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "game in context", func() bool { return recent("Won level 1") })
	view := l.ctx.Project(domain.CellGame)
	if len(view.Recent) == 0 {
		t.Fatal("the Game card needs to see what's happening elsewhere")
	}
}

func TestRecommendationCantUndoAnExplicitRequest(t *testing.T) {
	g := newRig(t)
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	before := gameID(l)
	say(t, l, "give me a game")
	waitFor(t, "new game", func() bool { return gameID(l) != before })
	now := gameID(l)
	l.orch.Handle([]evaluator.Recommendation{{Action: domain.ActUpdate, TargetType: domain.CellGame, CellType: domain.CellGame, Objective: "something else", Reason: "r"}})
	time.Sleep(50 * time.Millisecond)
	l.orch.Tick()
	time.Sleep(30 * time.Millisecond)
	if gameID(l) != now || len(l.Workspace().Pending) != 0 {
		t.Fatalf("the requested game was replaced or queued: %+v", l.Workspace().Pending)
	}
}

func historyRig(t *testing.T, title string) *rig {
	t.Helper()
	g := newRig(t)
	p, err := g.st.CreatePursuit(title, "", 7)
	if err != nil {
		t.Fatal(err)
	}
	g.pid = p.ID
	return g
}

func TestHistorySubjectStartsWithAMap(t *testing.T) {
	g := historyRig(t, "The Mughal Empire")
	l, err := g.m.Start(g.pid, "")
	if err != nil {
		t.Fatal(err)
	}
	ready(t, l)
	if cell(l, domain.CellMap).ID == "" || cell(l, domain.CellConcept).ID != "" || len(l.Workspace().Cells) != 6 {
		t.Fatalf("%+v", l.Workspace().Cells)
	}
}

func TestOtherSubjectsHaveNoMap(t *testing.T) {
	g := newRig(t)
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	if cell(l, domain.CellMap).ID != "" {
		t.Fatal("forces doesn't need a map")
	}
}

func TestModelCanSpotAMapSubjectTheWordsMissed(t *testing.T) {
	g := historyRig(t, "Akbar")
	g.mock.Override("journey.index", `{"topics":[{"title":"Early life","milestone":"Scout"}],"uses_maps":true}`)
	l, err := g.m.Start(g.pid, "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "map replaces key idea", func() bool { return cell(l, domain.CellMap).ID != "" })
	ready(t, l)
	if cell(l, domain.CellConcept).ID != "" {
		t.Fatal("the key idea should make way")
	}
	if len(l.Workspace().Cells) != 6 {
		t.Fatalf("%d", len(l.Workspace().Cells))
	}
}

func TestAskingForAMapInAnySubjectAddsOne(t *testing.T) {
	g := newRig(t)
	l, _ := g.m.Start(g.pid, "Why?")
	ready(t, l)
	say(t, l, "show me on a map")
	waitFor(t, "map", func() bool { return cell(l, domain.CellMap).ID != "" && len(cell(l, domain.CellMap).Content) > 0 })
}

func TestTappingAPlaceReachesContextWithoutAModelCall(t *testing.T) {
	g := historyRig(t, "The Mughal Empire")
	l, _ := g.m.Start(g.pid, "")
	ready(t, l)
	before := g.mock.Calls("evaluator.assess")
	b, _ := json.Marshal(map[string]string{"place": "Agra"})
	if err := l.Action(cell(l, domain.CellMap).ID, domain.EvMapExplored, b, "m1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "context", func() bool {
		for _, r := range l.ctx.Snapshot().Recent {
			if strings.Contains(r.Text, "Agra") {
				return true
			}
		}
		return false
	})
	time.Sleep(30 * time.Millisecond)
	if g.mock.Calls("evaluator.assess") != before {
		t.Fatal("tapping a pin shouldn't cost a model call")
	}
	if e, _ := g.st.ListEvidence(g.pid); len(e) != 0 {
		t.Fatal("looking at a map isn't evidence of understanding")
	}
}
