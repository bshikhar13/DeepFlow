package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deepflow/internal/domain"
	"deepflow/internal/orchestrator"
	"deepflow/internal/provider"
	"deepflow/internal/session"
	"deepflow/internal/store"
)

type rig struct {
	t    *testing.T
	srv  *httptest.Server
	st   *store.Store
	mock *provider.Mock
	mgr  *session.Manager
	path string
}

func cfgFor(st *store.Store, p provider.Provider) session.Config {
	return session.Config{Store: st, Provider: p, Coalesce: 5 * time.Millisecond, Policy: orchestrator.DefaultPolicy(), TickEvery: 10 * time.Millisecond}
}

func newRig(t *testing.T) *rig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	g := &rig{t: t, st: st, mock: provider.NewMock(), path: path}
	g.mgr = session.NewManager(cfgFor(st, g.mock))
	g.srv = httptest.NewServer(New(g.mgr, st, g.mock))
	t.Cleanup(func() {
		g.srv.Close()
		g.mgr.Shutdown()
		st.Close()
	})
	return g
}

// do sends a request. The test server's Host is loopback, so it passes checks.
func (g *rig) do(method, path string, body any, out any) int {
	g.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, g.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		g.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

type stateResp struct {
	Session   domain.Session        `json:"session"`
	Workspace domain.WorkspaceState `json:"workspace"`
	Messages  []domain.Message      `json:"messages"`
	Drafts    map[string]string     `json:"drafts"`
	Artifacts []domain.Artifact     `json:"artifacts"`
}

func (g *rig) start() (string, string) {
	g.t.Helper()
	var p domain.Pursuit
	if c := g.do("POST", "/api/pursuits", map[string]any{"title": "Force and Motion", "inquiry": "Why do things move?", "grade": 7}, &p); c != 201 {
		g.t.Fatalf("create pursuit: %d", c)
	}
	var s stateResp
	if c := g.do("POST", "/api/pursuits/"+p.ID+"/sessions", map[string]string{"inquiry": "Why do things move?"}, &s); c != 200 {
		g.t.Fatalf("start session: %d", c)
	}
	return p.ID, s.Session.ID
}

func (g *rig) state(sid string) stateResp {
	g.t.Helper()
	var s stateResp
	if c := g.do("GET", "/api/sessions/"+sid, nil, &s); c != 200 {
		g.t.Fatalf("state: %d", c)
	}
	return s
}

func (g *rig) ready(sid string) stateResp {
	g.t.Helper()
	var s stateResp
	waitFor(g.t, "cells populated", func() bool {
		s = g.state(sid)
		if len(s.Workspace.Cells) == 0 {
			return false
		}
		for _, c := range s.Workspace.Cells {
			if len(c.Content) == 0 || c.Status != domain.StatusWaiting {
				return false
			}
		}
		return true
	})
	return s
}

func cellOf(s stateResp, t domain.CellType) domain.Cell {
	for _, c := range s.Workspace.Cells {
		if c.Type == t {
			return c
		}
	}
	return domain.Cell{}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	dl := time.Now().Add(5 * time.Second)
	for time.Now().Before(dl) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

func (g *rig) act(cellID string, typ domain.EventType, text, clientID string) int {
	g.t.Helper()
	return g.do("POST", "/api/cells/"+cellID+"/action", map[string]any{"type": typ, "payload": map[string]string{"text": text}, "client_id": clientID}, nil)
}

func TestRejectsNonLoopbackHost(t *testing.T) {
	g := newRig(t)
	req, _ := http.NewRequest("GET", g.srv.URL+"/api/pursuits", nil)
	req.Host = "evil.example.com"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestRejectsCrossSiteWrites(t *testing.T) {
	g := newRig(t)
	req, _ := http.NewRequest("POST", g.srv.URL+"/api/pursuits", strings.NewReader(`{"title":"x"}`))
	req.Header.Set("Origin", "https://evil.example.com")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestCreatePursuitValidatesTitle(t *testing.T) {
	g := newRig(t)
	if c := g.do("POST", "/api/pursuits", map[string]string{"title": "   "}, nil); c != 400 {
		t.Fatalf("status %d", c)
	}
}

func TestCreatePursuitStartsSevenCells(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	if len(s.Workspace.Cells) != 6 {
		t.Fatalf("%d cells", len(s.Workspace.Cells))
	}
	for _, c := range s.Workspace.Cells {
		if c.AgentInstanceID == "" || c.Objective == "" {
			t.Fatalf("%+v", c)
		}
	}
	if len(s.Messages) == 0 {
		t.Fatal("dialogue should open with a message")
	}
}

func TestDialogueUsableBeforeOtherCellsReady(t *testing.T) {
	g := newRig(t)
	g.mock.Delay("concept.content", 300*time.Millisecond)
	_, sid := g.start()
	s := g.state(sid)
	d := cellOf(s, domain.CellDialogue)
	if c := g.act(d.ID, domain.EvUserMessage, "hello", "x1"); c != 202 {
		t.Fatalf("status %d", c)
	}
}

func TestBlankAndOversizedInputRejected(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	d := cellOf(s, domain.CellDialogue)
	before, _ := g.st.ListEvents(sid, domain.EvUserMessage)
	if c := g.act(d.ID, domain.EvUserMessage, " \n\t ", "b1"); c != 400 {
		t.Fatalf("blank: %d", c)
	}
	if c := g.act(d.ID, domain.EvUserMessage, strings.Repeat("x", 20001), "b2"); c != 400 {
		t.Fatalf("oversized: %d", c)
	}
	after, _ := g.st.ListEvents(sid, domain.EvUserMessage)
	if len(after) != len(before) {
		t.Fatal("rejected input created an event")
	}
}

func TestWrongActionForCellRejected(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	if c := g.act(cellOf(s, domain.CellConcept).ID, domain.EvQuizAnswered, "x", "w1"); c != 400 {
		t.Fatalf("status %d", c)
	}
}

func TestDuplicateClientIDIgnored(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	d := cellOf(s, domain.CellDialogue)
	for i := 0; i < 2; i++ {
		if c := g.act(d.ID, domain.EvUserMessage, "same message", "dup"); c != 202 {
			t.Fatalf("status %d", c)
		}
	}
	time.Sleep(30 * time.Millisecond)
	n := 0
	for _, m := range g.state(sid).Messages {
		if m.Role == "user" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d user messages", n)
	}
}

func TestStaleObjectPatchConflict(t *testing.T) {
	g := newRig(t)
	pid, sid := g.start()
	s := g.ready(sid)
	g.act(cellOf(s, domain.CellDialogue).ID, domain.EvUserMessage, "Indexes always make searches faster", "m1")
	var objs []domain.ConceptObject
	waitFor(t, "object", func() bool {
		g.do("GET", "/api/pursuits/"+pid+"/objects", nil, &objs)
		return len(objs) > 0
	})
	o := objs[0]
	if c := g.do("PATCH", "/api/objects/"+o.ID, map[string]any{"content": "new", "version": o.Version}, nil); c != 200 {
		t.Fatalf("status %d", c)
	}
	var cur struct {
		Current domain.ConceptObject `json:"current"`
	}
	if c := g.do("PATCH", "/api/objects/"+o.ID, map[string]any{"content": "stale", "version": o.Version}, &cur); c != 409 {
		t.Fatalf("status %d", c)
	}
	if cur.Current.Content != "new" || cur.Current.Authorship != domain.AuthorEdited {
		t.Fatalf("%+v", cur.Current)
	}
}

func TestKillMidStreamKeepsPartial(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	g.mock.SetStreamStepDelay(10 * time.Millisecond)
	g.mock.Override(provider.StreamName, strings.Repeat("word ", 500))
	g.act(cellOf(s, domain.CellDialogue).ID, domain.EvUserMessage, "tell me", "k1")
	waitFor(t, "partial", func() bool {
		ms, _ := g.st.ListMessages(sid)
		last := ms[len(ms)-1]
		return last.Role == "assistant" && len(last.Content) > 5
	})
	g.mgr.Shutdown()

	m2 := session.NewManager(cfgFor(g.st, g.mock))
	defer m2.Shutdown()
	srv2 := httptest.NewServer(New(m2, g.st, g.mock))
	defer srv2.Close()
	g.srv.Close()
	g.srv = srv2
	st := g.state(sid)
	var user, partial bool
	for _, m := range st.Messages {
		if m.Role == "user" && m.Content == "tell me" {
			user = true
		}
		if m.Role == "assistant" && m.Interrupted && strings.HasPrefix(m.Content, "word") {
			partial = true
		}
	}
	if !user || !partial {
		t.Fatalf("user %v partial %v: %+v", user, partial, st.Messages)
	}
}

func TestStopEndsStreamWithoutFailure(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	d := cellOf(s, domain.CellDialogue)
	g.mock.SetStreamStepDelay(10 * time.Millisecond)
	g.mock.Override(provider.StreamName, strings.Repeat("word ", 500))
	g.act(d.ID, domain.EvUserMessage, "tell me", "s1")
	waitFor(t, "streaming", func() bool {
		ms, _ := g.st.ListMessages(sid)
		return ms[len(ms)-1].Role == "assistant" && len(ms[len(ms)-1].Content) > 5
	})
	if c := g.do("POST", "/api/cells/"+d.ID+"/stop", nil, nil); c != 202 {
		t.Fatalf("status %d", c)
	}
	waitFor(t, "stopped", func() bool { return cellOf(g.state(sid), domain.CellDialogue).Status == domain.StatusWaiting })
	g.mock.SetStreamStepDelay(0)
	if cellOf(g.state(sid), domain.CellDialogue).Error != "" {
		t.Fatal("stop is not an error")
	}
}

func TestRestartRestoresWorkspaceAndDrafts(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	pra := cellOf(s, domain.CellQuiz)
	if c := g.do("PUT", "/api/cells/"+pra.ID+"/draft", map[string]string{"content": "half an answer"}, nil); c != 204 {
		t.Fatalf("status %d", c)
	}
	g.do("POST", "/api/cells/"+pra.ID+"/pin", nil, nil)
	calls := g.mock.Calls("concept.content")
	g.mgr.Shutdown()

	m2 := session.NewManager(cfgFor(g.st, g.mock))
	defer m2.Shutdown()
	srv2 := httptest.NewServer(New(m2, g.st, g.mock))
	defer srv2.Close()
	g.srv.Close()
	g.srv = srv2
	st := g.ready(sid)
	if st.Drafts[pra.ID] != "half an answer" {
		t.Fatalf("%v", st.Drafts)
	}
	if len(st.Workspace.Cells) != 6 || !cellOf(st, domain.CellQuiz).Pinned {
		t.Fatalf("%+v", st.Workspace)
	}
	if g.mock.Calls("concept.content") != calls {
		t.Fatal("restore regenerated content")
	}
}

func TestRemoveRetainsArtifact(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	pra := cellOf(s, domain.CellQuiz)
	g.do("PUT", "/api/cells/"+pra.ID+"/draft", map[string]string{"content": "my unfinished thinking"}, nil)
	if c := g.do("POST", "/api/cells/"+pra.ID+"/remove", nil, nil); c != 204 {
		t.Fatalf("status %d", c)
	}
	st := g.state(sid)
	if cellOf(st, domain.CellQuiz).ID != "" {
		t.Fatal("cell still present")
	}
	var found bool
	for _, a := range st.Artifacts {
		if a.LearnerAuthored && strings.Contains(string(a.Content), "my unfinished thinking") {
			found = true
		}
	}
	if !found {
		t.Fatalf("%+v", st.Artifacts)
	}
}

func TestDialogueRemoveRejected(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	if c := g.do("POST", "/api/cells/"+cellOf(s, domain.CellDialogue).ID+"/remove", nil, nil); c != 409 {
		t.Fatalf("status %d", c)
	}
}

func TestDirectionShiftReplacesCell(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	con := cellOf(s, domain.CellConcept)
	g.act(cellOf(s, domain.CellDialogue).ID, domain.EvUserMessage, "What if there was no friction?", "o1")
	waitFor(t, "replaced", func() bool { return cellOf(g.state(sid), domain.CellConcept).ID == "" })
	st := g.state(sid)
	if len(st.Workspace.Cells) != 6 {
		t.Fatalf("%d", len(st.Workspace.Cells))
	}
	found := false
	for _, a := range st.Artifacts {
		found = found || a.CellID == con.ID
	}
	if !found {
		t.Fatal("artifact not preserved")
	}
}

func TestPinSurvivesAdaptation(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	con := cellOf(s, domain.CellConcept)
	if c := g.do("POST", "/api/cells/"+con.ID+"/pin", nil, nil); c != 204 {
		t.Fatalf("status %d", c)
	}
	g.act(cellOf(s, domain.CellDialogue).ID, domain.EvUserMessage, "What if there was no friction?", "o1")
	waitFor(t, "evaluation", func() bool { return g.mock.Calls("evaluator.assess") >= 1 })
	time.Sleep(60 * time.Millisecond)
	if cellOf(g.state(sid), domain.CellConcept).ID != con.ID {
		t.Fatal("pinned cell replaced")
	}
}

func TestBusyCellNotReplacedUntilLeaseEnds(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	con := cellOf(s, domain.CellConcept)
	if c := g.do("POST", "/api/cells/"+con.ID+"/busy", map[string]any{"busy": true}, nil); c != 204 {
		t.Fatalf("status %d", c)
	}
	g.act(cellOf(s, domain.CellDialogue).ID, domain.EvUserMessage, "What if there was no friction?", "o1")
	waitFor(t, "evaluation", func() bool { return g.mock.Calls("evaluator.assess") >= 1 })
	time.Sleep(60 * time.Millisecond)
	if cellOf(g.state(sid), domain.CellConcept).ID != con.ID {
		t.Fatal("busy cell replaced")
	}
	g.do("POST", "/api/cells/"+con.ID+"/busy", map[string]any{"busy": false}, nil)
	waitFor(t, "applied after release", func() bool { return cellOf(g.state(sid), domain.CellConcept).ID == "" })
}

func TestUndoRoute(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	con := cellOf(s, domain.CellConcept)
	g.do("POST", "/api/cells/"+con.ID+"/remove", nil, nil)
	if c := g.do("POST", "/api/sessions/"+sid+"/undo", nil, nil); c != 204 {
		t.Fatalf("status %d", c)
	}
	if cellOf(g.state(sid), domain.CellConcept).ID != con.ID {
		t.Fatal("not restored")
	}
	if c := g.do("POST", "/api/sessions/"+sid+"/undo", nil, nil); c != 409 {
		t.Fatalf("status %d", c)
	}
}

func TestOneCellActionLeavesOthersUsable(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	g.mock.Delay("game.spec", 400*time.Millisecond)
	if c := g.do("POST", "/api/cells/"+cellOf(s, domain.CellGame).ID+"/action", map[string]any{"type": domain.EvGameNext, "client_id": "g1"}, nil); c != 202 {
		t.Fatalf("status %d", c)
	}
	quiz := cellOf(s, domain.CellQuiz)
	var view struct {
		ID string `json:"id"`
	}
	raw, _ := json.Marshal(quiz.Content)
	json.Unmarshal(raw, &view)
	if c := g.do("POST", "/api/cells/"+quiz.ID+"/action", map[string]any{"type": domain.EvQuizAnswered, "payload": map[string]any{"questionId": view.ID, "choice": 0}, "client_id": "q1"}, nil); c != 202 {
		t.Fatalf("status %d", c)
	}
	waitFor(t, "quiz answered", func() bool {
		return strings.Contains(string(cellOf(g.state(sid), domain.CellQuiz).Content), `"result"`)
	})
	if cellOf(g.state(sid), domain.CellGame).Status != domain.StatusUpdating {
		t.Fatal("the game should still be working")
	}
}

func TestPursuitClassLevelValidated(t *testing.T) {
	g := newRig(t)
	var p domain.Pursuit
	if c := g.do("POST", "/api/pursuits", map[string]any{"title": "T", "grade": 9}, &p); c != 201 || p.Grade != 9 {
		t.Fatalf("%d %+v", c, p)
	}
	for _, bad := range []int{5, 13, -1} {
		if c := g.do("POST", "/api/pursuits", map[string]any{"title": "T", "grade": bad}, nil); c != 400 {
			t.Errorf("grade %d: status %d", bad, c)
		}
	}
	if c := g.do("POST", "/api/pursuits", map[string]any{"title": "T"}, &p); c != 201 || p.Grade != store.DefaultGrade {
		t.Fatalf("no class given: %d %+v", c, p)
	}
}

func TestMilestoneStreamEvent(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	game := cellOf(s, domain.CellGame).ID
	wins := []map[string]any{
		{"level": 0, "values": map[string]float64{"force": 50, "mass": 10}},
		{"level": 1, "values": map[string]float64{"force": 20, "mass": 10}},
		{"level": 2, "values": map[string]float64{"force": 60, "mass": 5}},
	}
	for i, w := range wins {
		if c := g.do("POST", "/api/cells/"+game+"/action", map[string]any{"type": domain.EvGameLevelWon, "payload": w, "client_id": fmt.Sprint("w", i)}, nil); c != 202 {
			t.Fatalf("status %d", c)
		}
	}
	got := readSSE(t, g.srv.URL+"/api/sessions/"+sid+"/stream", "", func(id, ev string) bool { return ev == "milestone.reached" })
	if !strings.HasSuffix(got[len(got)-1], "milestone.reached") {
		t.Fatalf("%v", got[max(0, len(got)-3):])
	}
}

func TestEvidenceReachesUnderstandingCell(t *testing.T) {
	g := newRig(t)
	pid, sid := g.start()
	s := g.ready(sid)
	g.act(cellOf(s, domain.CellDialogue).ID, domain.EvUserMessage, "A heavier cart speeds up less because the same push has to move a lot more mass.", "e1")
	waitFor(t, "understanding updated", func() bool {
		return strings.Contains(string(cellOf(g.state(sid), domain.CellJourney).Content), "strong")
	})
	var ev []domain.Evidence
	g.do("GET", "/api/pursuits/"+pid+"/evidence", nil, &ev)
	if len(ev) != 1 || ev[0].SourceEventID == "" {
		t.Fatalf("%+v", ev)
	}
}

type claudeNamed struct{ *provider.Mock }

func (claudeNamed) Name() string { return "claude" }

func TestNoClaudeCallsBeforeConsent(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := provider.NewMock()
	p := claudeNamed{m}
	mgr := session.NewManager(cfgFor(st, p))
	defer mgr.Shutdown()
	srv := httptest.NewServer(New(mgr, st, p))
	defer srv.Close()
	g := &rig{t: t, srv: srv, st: st, mock: m, mgr: mgr}
	var status map[string]any
	g.do("GET", "/api/status", nil, &status)
	if status["provider"] != "claude" || status["consentRequired"] != true {
		t.Fatalf("%v", status)
	}
	var pur domain.Pursuit
	g.do("POST", "/api/pursuits", map[string]string{"title": "T"}, &pur)
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if c := g.do("POST", "/api/pursuits/"+pur.ID+"/sessions", map[string]string{}, &e); c != 409 || e.Error.Code != "consent_required" {
		t.Fatalf("%d %+v", c, e)
	}
	time.Sleep(20 * time.Millisecond)
	if m.Calls("concept.content")+m.Calls(provider.StreamName) != 0 {
		t.Fatal("model called without consent")
	}
	if c := g.do("POST", "/api/consent", nil, nil); c != 204 {
		t.Fatalf("consent %d", c)
	}
	if c := g.do("POST", "/api/pursuits/"+pur.ID+"/sessions", map[string]string{}, nil); c != 200 {
		t.Fatalf("after consent %d", c)
	}
}

func TestMetricsCountCalls(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	g.ready(sid)
	var m map[string]float64
	if c := g.do("GET", "/api/sessions/"+sid+"/metrics", nil, &m); c != 200 {
		t.Fatalf("status %d", c)
	}
	if m["calls"] < 1 {
		t.Fatalf("%v", m)
	}
}

// readSSE collects events until the deadline or stop returns true.
func readSSE(t *testing.T, url, lastID string, stop func(id, event string) bool) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var ids []string
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	id := ""
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "id: ") {
			id = line[4:]
		}
		if strings.HasPrefix(line, "event: ") {
			ids = append(ids, id+":"+line[7:])
			if stop(id, line[7:]) {
				return ids
			}
		}
	}
	return ids
}

func TestSSEReplayFromLastEventID(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	g.ready(sid)
	url := g.srv.URL + "/api/sessions/" + sid + "/stream"
	got := readSSE(t, url, "1", func(id, ev string) bool { return id == "3" })
	if len(got) < 2 || !strings.HasPrefix(got[0], "2:") {
		t.Fatalf("%v", got)
	}
}

func TestSSEGapSignalsResync(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	g.ready(sid)
	url := g.srv.URL + "/api/sessions/" + sid + "/stream"
	got := readSSE(t, url, fmt.Sprint(99999), func(id, ev string) bool { return ev == "resync" })
	if len(got) == 0 || !strings.HasSuffix(got[0], "resync") {
		t.Fatalf("%v", got)
	}
}

func TestRestartKeepsActivityAndDeferredChanges(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	con := cellOf(s, domain.CellConcept)
	pra := cellOf(s, domain.CellQuiz)
	g.do("POST", "/api/cells/"+pra.ID+"/busy", map[string]any{"busy": true}, nil)
	g.act(cellOf(s, domain.CellDialogue).ID, domain.EvUserMessage, "What if there was no friction?", "o1")
	waitFor(t, "replaced", func() bool { return cellOf(g.state(sid), domain.CellConcept).ID != con.ID })
	waitFor(t, "update deferred", func() bool { return len(g.state(sid).Workspace.Pending) == 1 })
	g.mgr.Shutdown()

	m2 := session.NewManager(cfgFor(g.st, g.mock))
	defer m2.Shutdown()
	srv2 := httptest.NewServer(New(m2, g.st, g.mock))
	defer srv2.Close()
	g.srv.Close()
	g.srv = srv2
	var st struct {
		Decisions []struct {
			Decision domain.Decision `json:"decision"`
			CanUndo  bool            `json:"canUndo"`
		} `json:"decisions"`
		Workspace domain.WorkspaceState `json:"workspace"`
	}
	g.do("GET", "/api/sessions/"+sid, nil, &st)
	if len(st.Decisions) == 0 || st.Decisions[0].Decision.Reason == "" {
		t.Fatalf("activity lost: %+v", st.Decisions)
	}
	// Busy leases don't survive a restart, so the deferred update now goes through.
	waitFor(t, "deferred update applied", func() bool {
		return strings.Contains(cellOf(g.state(sid), domain.CellQuiz).Objective, "what-if")
	})
}

func TestConsentIsPerProvider(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := provider.NewMock()
	claude := session.NewManager(cfgFor(st, claudeNamed{m}))
	defer claude.Shutdown()
	openai := session.NewManager(cfgFor(st, openaiNamed{m}))
	defer openai.Shutdown()
	if err := claude.GrantConsent(); err != nil {
		t.Fatal(err)
	}
	if claude.ConsentNeeded() {
		t.Fatal("claude consent should be recorded")
	}
	if !openai.ConsentNeeded() {
		t.Fatal("accepting Anthropic must not count as accepting OpenAI")
	}
}

type openaiNamed struct{ *provider.Mock }

func (openaiNamed) Name() string { return "openai" }

type remoteStub struct{ *provider.Mock }

func (remoteStub) Name() string      { return "openai" }
func (remoteStub) Vendor() string    { return "OpenAI" }
func (remoteStub) ModelName() string { return "gpt-test" }

func TestStatusNamesVendorAndModel(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := remoteStub{provider.NewMock()}
	mgr := session.NewManager(cfgFor(st, p))
	defer mgr.Shutdown()
	srv := httptest.NewServer(New(mgr, st, p))
	defer srv.Close()
	g := &rig{t: t, srv: srv}
	var status map[string]any
	g.do("GET", "/api/status", nil, &status)
	if status["vendor"] != "OpenAI" || status["model"] != "gpt-test" || status["consentRequired"] != true {
		t.Fatalf("%v", status)
	}
}

func rawGet(t *testing.T, url string) string {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return string(b)
}

func TestAnswersNeverReachTheBrowser(t *testing.T) {
	g := newRig(t)
	_, sid := g.start()
	s := g.ready(sid)
	dlg := cellOf(s, domain.CellDialogue).ID
	g.act(dlg, domain.EvUserMessage, "What if there was no friction?", "w1") // spawns a What If with a hidden reveal
	waitFor(t, "what if", func() bool {
		c := cellOf(g.state(sid), domain.CellThought)
		return c.ID != "" && c.Content != nil
	})
	state := rawGet(t, g.srv.URL+"/api/sessions/"+sid)
	if strings.Contains(state, "localState") || strings.Contains(state, `"answer"`) || strings.Contains(state, `"reveal"`) {
		t.Fatalf("session state leaks hidden data: %s", state[:300])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", g.srv.URL+"/api/sessions/"+sid+"/stream?after=0", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if len(body) == 0 {
		t.Fatal("no stream data")
	}
	if strings.Contains(string(body), "localState") || strings.Contains(string(body), `"answer":`+"0") || strings.Contains(string(body), `"queue"`) || strings.Contains(string(body), `"reveal"`) {
		t.Fatalf("stream leaks hidden data")
	}
	// The hidden state must still survive a restart, or the quiz breaks.
	sess, _ := g.st.GetSession(sid)
	if !strings.Contains(string(sess.Snapshot), "localState") {
		t.Fatal("saved snapshot lost the agent state")
	}
}
