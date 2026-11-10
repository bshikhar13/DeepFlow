package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"deepflow/internal/agents"
	"deepflow/internal/domain"
	"deepflow/internal/evaluator"
	"deepflow/internal/harness"
	"deepflow/internal/orchestrator"
	"deepflow/internal/provider"
)

// MaxInputRunes is the longest learner text accepted in one action.
const MaxInputRunes = 20000

const flushEvery = 250 * time.Millisecond

var (
	// ErrInvalidInput marks learner input that can't be accepted.
	ErrInvalidInput = errors.New("invalid input")
	// ErrClosed means the session is no longer live.
	ErrClosed = errors.New("session is not live")
)

var allowedActions = map[domain.CellType]map[domain.EventType]bool{
	domain.CellDialogue: {domain.EvUserMessage: true},
	domain.CellQuiz:     {domain.EvQuizAnswered: true, domain.EvQuizNext: true},
	domain.CellGame:     {domain.EvGameLevelWon: true, domain.EvGameNext: true},
	domain.CellThought:  {domain.EvThoughtSubmitted: true},
	domain.CellConcept:  {domain.EvConceptExplored: true},
	domain.CellMap:      {domain.EvMapExplored: true},
}

var needsText = map[domain.EventType]bool{domain.EvUserMessage: true, domain.EvThoughtSubmitted: true}

// quizBatchSize is how many answers go by between Evaluator looks at the quiz.
// Every answer is recorded, but a model call per question would be wasteful.
const quizBatchSize = 5

const maxRecent = 20

// Live wires together the harness for one active session. It is the Sink for
// agents and the Notifier for the Orchestrator, and turns both into stream events.
type Live struct {
	cfg     Config
	session domain.Session
	pursuit domain.Pursuit
	bus     *harness.Bus
	ctx     *harness.ContextStore
	rt      *harness.Runtime
	orch    *orchestrator.Orchestrator
	hub     *hub
	stopEv  func()
	unsub   func()
	done    chan struct{}
	gov     *harness.Governed

	mu        sync.Mutex
	closed    bool
	streamID  string
	streamBuf strings.Builder
	lastFlush time.Time
	decisions []Decided
	retrieval []RetrievalQuestion
	quizRight int
	quizTotal int
	// pendingSteps counts achievements earned before the index was ready.
	pendingSteps int
}

// Decided is a logged workspace decision for the activity panel.
type Decided struct {
	Decision domain.Decision `json:"decision"`
	CanUndo  bool            `json:"canUndo"`
	At       time.Time       `json:"at"`
}

func newLive(cfg Config, s domain.Session, p domain.Pursuit, seed harness.LearningContext, sched *harness.Scheduler) *Live {
	l := &Live{cfg: cfg, session: s, pursuit: p, hub: newHub(), done: make(chan struct{})}
	l.bus = harness.NewBus(cfg.Store)
	l.ctx = harness.NewContextStore(seed)
	metrics := &harness.Metrics{}
	l.rt = harness.NewRuntime(harness.RuntimeConfig{
		SessionID: s.ID, Provider: cfg.Provider, Scheduler: sched, AgentBudget: cfg.AgentBudget,
		TaskTimeout: cfg.TaskTimeout, History: l.history, Metrics: metrics,
	}, l.bus, l.ctx, agents.New, l)
	l.gov = &harness.Governed{P: cfg.Provider, Sched: sched, Metrics: metrics}
	l.orch = orchestrator.New(orchestrator.Config{
		SessionID: s.ID, Inquiry: s.Inquiry, Policy: cfg.Policy, Engine: l.rt, Store: cfg.Store,
		Bus: l.bus, Context: l.ctx, Notifier: l,
	})
	l.unsub = l.bus.Subscribe(l.onEvent)
	ev := evaluator.New(evaluator.Config{
		SessionID: s.ID, PursuitID: p.ID, Coalesce: cfg.Coalesce, Provider: l.gov, Store: cfg.Store,
		Context: l.ctx, Bus: l.bus, OnResult: func(o evaluator.Output) { l.orch.Handle(o.Recommendations) },
	})
	l.stopEv = ev.Start()
	go l.tickLoop()
	return l
}

func (l *Live) tickLoop() {
	t := time.NewTicker(l.cfg.TickEvery)
	defer t.Stop()
	for {
		select {
		case <-l.done:
			return
		case <-t.C:
			l.orch.Tick()
		}
	}
}

// SessionID returns the session's ID.
func (l *Live) SessionID() string { return l.session.ID }

// PursuitID returns the pursuit's ID.
func (l *Live) PursuitID() string { return l.pursuit.ID }

// Workspace returns the current workspace.
func (l *Live) Workspace() domain.WorkspaceState { return l.orch.Snapshot() }

// ContextObjects returns the Living Document objects in shared context.
func (l *Live) ContextObjects() []domain.ConceptObject { return l.ctx.Snapshot().Objects }

// Metrics returns inference counters for the session.
func (l *Live) Metrics() map[string]int64 {
	m := l.rt.Metrics()
	return map[string]int64{"calls": m.Calls.Load(), "inputTokens": m.InputTokens.Load(), "outputTokens": m.OutputTokens.Load(), "failures": m.Failures.Load()}
}

// Subscribe attaches a stream client.
func (l *Live) Subscribe(lastSeq int64) ([]HubEvent, bool, chan HubEvent, func()) {
	return l.hub.subscribe(lastSeq)
}

// Decisions returns the activity log.
func (l *Live) Decisions() []Decided {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Decided{}, l.decisions...)
}

func (l *Live) history() []provider.Turn {
	msgs, err := l.cfg.Store.ListMessages(l.session.ID)
	if err != nil {
		log.Printf("session: history: %v", err)
		return nil
	}
	turns := make([]provider.Turn, 0, len(msgs))
	for _, m := range msgs {
		if m.Content != "" {
			turns = append(turns, provider.Turn{Role: m.Role, Content: m.Content})
		}
	}
	// A request must end with the learner's turn: partial replies after it are dropped.
	for len(turns) > 0 && turns[len(turns)-1].Role != "user" {
		turns = turns[:len(turns)-1]
	}
	return turns
}

// Action validates and publishes a learner action on a cell. Duplicate
// client IDs are ignored by the bus.
func (l *Live) Action(cellID string, t domain.EventType, payload json.RawMessage, clientID string) error {
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if closed {
		return ErrClosed
	}
	var cell *domain.Cell
	ws := l.orch.Snapshot()
	for i := range ws.Cells {
		if ws.Cells[i].ID == cellID {
			cell = &ws.Cells[i]
		}
	}
	if cell == nil {
		return orchestrator.ErrNoCell
	}
	if !allowedActions[cell.Type][t] {
		return fmt.Errorf("%w: %s doesn't accept %s", ErrInvalidInput, cell.Type, t)
	}
	if needsText[t] {
		var p struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(payload, &p)
		if strings.TrimSpace(p.Text) == "" {
			return fmt.Errorf("%w: text is empty", ErrInvalidInput)
		}
		if utf8.RuneCountInString(p.Text) > MaxInputRunes {
			return fmt.Errorf("%w: text is longer than %d characters", ErrInvalidInput, MaxInputRunes)
		}
	}
	if t == domain.EvQuizAnswered || t == domain.EvGameLevelWon || t == domain.EvQuizNext || t == domain.EvGameNext {
		if len(payload) == 0 {
			payload = json.RawMessage(`{}`)
		}
	}
	if t == domain.EvConceptExplored {
		var p struct {
			Concept string `json:"concept"`
		}
		_ = json.Unmarshal(payload, &p)
		if strings.TrimSpace(p.Concept) == "" {
			return fmt.Errorf("%w: concept is empty", ErrInvalidInput)
		}
	}
	e := domain.Event{SessionID: l.session.ID, Type: t, Origin: cellID, Payload: payload}
	if clientID != "" {
		e.DedupeKey = l.session.ID + ":" + clientID
	}
	return l.bus.Publish(e)
}

// onEvent routes persisted events. It runs once per event because the bus
// discards duplicates.
func (l *Live) onEvent(e domain.Event) {
	if e.SessionID != l.session.ID {
		return
	}
	switch e.Type {
	case domain.EvUserMessage:
		var p struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		m := domain.Message{SessionID: l.session.ID, Role: "user", Content: p.Text}
		if err := l.cfg.Store.AddMessage(&m); err != nil {
			log.Printf("session: save message: %v", err)
		}
		l.hub.publish("message.done", m)
		l.note("chat", p.Text)
		l.rt.Dispatch(e.Origin, e)
		if t, ok := DetectCardRequest(p.Text); ok {
			l.orch.UserRequest(t, "The student asked in chat: "+p.Text, "You asked for it, so here it is.")
		}
	case domain.EvThoughtSubmitted:
		l.note("what-if guess", payloadField(e, "text"))
		l.rt.BumpUserRevision(e.Origin)
		l.rt.Dispatch(e.Origin, e)
	case domain.EvConceptExplored:
		l.note("explored", payloadField(e, "concept"))
		l.rt.Dispatch(e.Origin, e)
	case domain.EvQuizAnswered, domain.EvQuizNext, domain.EvGameLevelWon, domain.EvGameNext:
		l.rt.Dispatch(e.Origin, e)
	case domain.EvMapExplored:
		// Shared with other cards, but looking at a map costs no model call and isn't evidence.
		if place := payloadField(e, "place"); place != "" {
			l.note("map", "Looked at "+place)
		}
	case domain.EvRetrievalAnswered:
		l.note("recalled", payloadField(e, "text"))
	case domain.EvQuizResult:
		l.onQuizResult(e)
	case domain.EvGameStar:
		var star struct {
			Game  string `json:"game"`
			Level int    `json:"level"`
		}
		_ = json.Unmarshal(e.Payload, &star)
		l.note("game", fmt.Sprintf("Won level %d of %s", star.Level+1, star.Game))
		l.addStep()
	case domain.EvJourneySet:
		l.onJourneySet(e)
	case domain.EvAgentMessage:
		var p struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		l.finishStream(p.Text, false)
	case domain.EvContextUpdated:
		l.directByType(domain.CellJourney)
		l.directByType(domain.CellDocument)
		l.hub.publish("document.changed", struct{}{})
	case domain.EvObjectEdited, domain.EvObjectChallenged:
		l.hub.publish("document.changed", struct{}{})
	}
}

// note shares an interaction with every card straight away. It doesn't wait for
// the Evaluator, which reads this context but may be slow or failing.
func (l *Live) note(kind, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	l.ctx.Apply(func(c *harness.LearningContext) {
		c.Recent = append(c.Recent, harness.Interaction{Kind: kind, Text: text})
		if n := len(c.Recent); n > maxRecent {
			c.Recent = c.Recent[n-maxRecent:]
		}
	})
}

func payloadField(e domain.Event, field string) string {
	var p map[string]any
	_ = json.Unmarshal(e.Payload, &p)
	s, _ := p[field].(string)
	return s
}

func (l *Live) onJourneySet(e domain.Event) {
	var j domain.Journey
	if err := json.Unmarshal(e.Payload, &j); err != nil {
		log.Printf("session: bad journey: %v", err)
		return
	}
	l.ctx.Apply(func(c *harness.LearningContext) { c.Journey = &j })
	if err := l.cfg.Store.SaveJourney(l.pursuit.ID, j); err != nil {
		log.Printf("session: save journey: %v", err)
	}
	if j.UsesMaps {
		l.addMap()
	}
	l.mu.Lock()
	owed := l.pendingSteps
	l.pendingSteps = 0
	l.mu.Unlock()
	for i := 0; i < owed; i++ {
		l.addStep()
	}
}

// addMap gives a subject a map if the words in its title didn't reveal it needed one.
func (l *Live) addMap() {
	for _, c := range l.orch.Snapshot().Cells {
		if c.Type == domain.CellMap {
			return
		}
	}
	l.orch.Handle([]evaluator.Recommendation{{
		Action: domain.ActReplace, TargetType: domain.CellConcept, CellType: domain.CellMap,
		Objective: "Show the places behind this subject", Reason: "This subject is about places, so here's a map.",
	}})
}

// onQuizResult records one answer as evidence. A multiple-choice answer can be
// a lucky guess, so it never counts for more than moderate.
func (l *Live) onQuizResult(e domain.Event) {
	var r agents.QuizResultEvent
	if err := json.Unmarshal(e.Payload, &r); err != nil {
		log.Printf("session: bad quiz result: %v", err)
		return
	}
	if r.Correct {
		l.note("quiz", "Right on: "+r.Question)
	} else {
		l.note("quiz", "Wrong on: "+r.Question)
	}
	ev := domain.Evidence{
		PursuitID: l.pursuit.ID, SessionID: l.session.ID, Concept: r.Topic, LearnerAction: string(domain.EvQuizAnswered),
		Tier: domain.TierWeak, Correctness: "incorrect", Reasoning: "na",
		Rationale: "Quiz: " + r.Question, Uncertainty: "One multiple-choice answer.", SourceEventID: r.SourceEventID,
	}
	if r.Correct {
		ev.Tier, ev.Correctness = domain.TierModerate, "correct"
	}
	if err := l.cfg.Store.AddEvidence(&ev); err != nil {
		log.Printf("session: quiz evidence: %v", err)
	}
	if r.Correct {
		text := r.Topic + ": got quiz questions right"
		l.ctx.Apply(func(c *harness.LearningContext) {
			for _, d := range c.Demonstrated {
				if d.Text == text {
					return // every agent's prompt carries this list, so keep it short
				}
			}
			c.Demonstrated = append(c.Demonstrated, harness.DemonstratedItem{Text: text, Tier: string(ev.Tier), Uncertainty: ev.Uncertainty, EvidenceIDs: []string{ev.ID}})
		})
		l.addStep()
	}
	l.mu.Lock()
	l.quizTotal++
	if r.Correct {
		l.quizRight++
	}
	var summary string
	if l.quizTotal >= quizBatchSize {
		summary = fmt.Sprintf("Got %d of the last %d quiz questions right on %s.", l.quizRight, l.quizTotal, r.Topic)
		l.quizRight, l.quizTotal = 0, 0
	}
	l.mu.Unlock()
	if summary != "" {
		b, _ := json.Marshal(map[string]string{"text": summary})
		if err := l.bus.Publish(domain.Event{SessionID: l.session.ID, Type: domain.EvQuizBatch, Origin: "quiz", Payload: b}); err != nil {
			log.Printf("session: quiz batch: %v", err)
		}
	}
}

// addStep credits one achievement to the current topic. Finishing a topic is a
// milestone, and the learning cards follow the student to the next one, through
// the Orchestrator so a card in active use isn't changed.
func (l *Live) addStep() {
	var finished *domain.JourneyTopic
	var saved *domain.Journey
	next := ""
	complete := false
	l.ctx.Apply(func(c *harness.LearningContext) {
		if c.Journey == nil {
			return
		}
		finished = c.Journey.AddStep()
		copyJ := *c.Journey
		copyJ.Topics = append([]domain.JourneyTopic{}, c.Journey.Topics...)
		saved = &copyJ
		if t, ok := c.Journey.CurrentTopic(); ok {
			next = t.Title
		} else {
			complete = true
		}
	})
	if saved == nil {
		l.mu.Lock()
		l.pendingSteps++
		l.mu.Unlock()
		return
	}
	if err := l.cfg.Store.SaveJourney(l.pursuit.ID, *saved); err != nil {
		log.Printf("session: save journey: %v", err)
	}
	l.directByType(domain.CellJourney)
	if finished == nil {
		return
	}
	l.hub.publish("milestone.reached", map[string]any{"title": finished.Title, "milestone": finished.Milestone, "next": next, "complete": complete})
	if complete {
		return
	}
	recs := make([]evaluator.Recommendation, 0, 3)
	for _, t := range []domain.CellType{domain.CellQuiz, domain.CellConcept, domain.CellGame} {
		recs = append(recs, evaluator.Recommendation{Action: domain.ActUpdate, TargetType: t, CellType: t, Objective: "Next topic: " + next, Reason: "New topic: " + next + "."})
	}
	l.orch.Handle(recs)
}

func (l *Live) directByType(t domain.CellType) {
	for _, c := range l.orch.Snapshot().Cells {
		if c.Type == t {
			l.rt.Direct(c.ID, harness.Directive{Kind: "update", Objective: c.Objective})
		}
	}
}

// --- harness.Sink ---

// CellUpdated implements harness.Sink.
func (l *Live) CellUpdated(c domain.Cell) {
	if c.Type == domain.CellDialogue && c.Status == domain.StatusFailed {
		l.finishStream("", true)
	}
	l.orch.MergeCell(c)
}

// CellStatus implements harness.Sink.
func (l *Live) CellStatus(id string, s domain.CellStatus) {
	if s == domain.StatusWaiting {
		// A dialogue task that ended without a final message was stopped.
		for _, c := range l.orch.Snapshot().Cells {
			if c.ID == id && c.Type == domain.CellDialogue {
				l.finishStream("", true)
			}
		}
	}
	l.orch.SetStatus(id, s)
}

// Delta implements harness.Sink.
func (l *Live) Delta(cellID, text string) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	if l.streamID == "" {
		m := domain.Message{SessionID: l.session.ID, Role: "assistant", Interrupted: true}
		if err := l.cfg.Store.AddMessage(&m); err != nil {
			log.Printf("session: start message: %v", err)
		}
		l.streamID = m.ID
		l.streamBuf.Reset()
		l.lastFlush = time.Now()
	}
	l.streamBuf.WriteString(text)
	id := l.streamID
	var flush string
	if time.Since(l.lastFlush) >= flushEvery {
		flush = l.streamBuf.String()
		l.lastFlush = time.Now()
	}
	l.mu.Unlock()
	if flush != "" {
		// Interrupted stays true until the reply completes, so a crash leaves it marked.
		if err := l.cfg.Store.UpdateMessage(id, flush, true); err != nil {
			log.Printf("session: flush message: %v", err)
		}
	}
	l.hub.publish("cell.delta", map[string]string{"cellId": cellID, "messageId": id, "text": text})
}

func (l *Live) finishStream(final string, interrupted bool) {
	l.mu.Lock()
	id := l.streamID
	if id == "" {
		l.mu.Unlock()
		return
	}
	if final == "" {
		final = l.streamBuf.String()
	}
	l.streamID = ""
	l.mu.Unlock()
	if err := l.cfg.Store.UpdateMessage(id, final, interrupted); err != nil {
		log.Printf("session: finish message: %v", err)
	}
	l.hub.publish("message.done", domain.Message{ID: id, SessionID: l.session.ID, Role: "assistant", Content: final, Interrupted: interrupted, CreatedAt: time.Now()})
}

// Request implements harness.Sink.
func (l *Live) Request(cellID string, r harness.CellRequest) {
	l.orch.Request(cellID, r)
}

// --- orchestrator.Notifier ---

// PublicCell removes what the browser must not see. An agent's local state holds
// unasked quiz questions with their answers, and What If reveals.
func PublicCell(c domain.Cell) domain.Cell {
	c.LocalState = nil
	return c
}

// PublicWorkspace applies PublicCell to a workspace.
func PublicWorkspace(ws domain.WorkspaceState) domain.WorkspaceState {
	cells := make([]domain.Cell, len(ws.Cells))
	for i, c := range ws.Cells {
		cells[i] = PublicCell(c)
	}
	ws.Cells = cells
	return ws
}

// WorkspaceChanged implements orchestrator.Notifier.
func (l *Live) WorkspaceChanged(ws domain.WorkspaceState) {
	l.hub.publish("layout.changed", PublicWorkspace(ws))
}

// CellChanged implements orchestrator.Notifier.
func (l *Live) CellChanged(c domain.Cell) { l.hub.publish("cell.updated", PublicCell(c)) }

// DecisionMade implements orchestrator.Notifier.
func (l *Live) DecisionMade(d domain.Decision, canUndo bool) {
	item := Decided{Decision: d, CanUndo: canUndo, At: time.Now()}
	l.mu.Lock()
	l.decisions = append(l.decisions, item)
	l.mu.Unlock()
	l.hub.publish("decision.made", item)
}

// --- user controls ---

// Pin protects a cell.
func (l *Live) Pin(id string, pinned bool) error {
	if pinned {
		return l.orch.UserPin(id)
	}
	return l.orch.UserUnpin(id)
}

// Busy renews or clears a cell's active-use lease.
func (l *Live) Busy(id string, busy bool) error {
	if busy {
		return l.orch.SetBusy(id, busyLease)
	}
	return l.orch.SetBusy(id, 0)
}

// Remove retires a cell at the learner's request. Its draft is kept as an artifact.
func (l *Live) Remove(id string) error { return l.orch.UserRemove(id) }

// Refresh asks a cell for an alternative.
func (l *Live) Refresh(id string) error { return l.orch.UserRefresh(id) }

// Retry replays a failed cell's work.
func (l *Live) Retry(id string) { l.rt.Retry(id) }

// Stop ends the cell's running task, keeping any partial output.
func (l *Live) Stop(id string) { l.rt.CancelCurrent(id) }

// Undo reverses the last structural change.
func (l *Live) Undo() error { return l.orch.Undo() }

// Reassess asks the Evaluator to re-evaluate the workspace.
func (l *Live) Reassess() error {
	return l.bus.Publish(domain.Event{SessionID: l.session.ID, Type: domain.EvUserReassess, Origin: "user"})
}

// ObjectsChanged tells this session the Living Document was edited.
func (l *Live) ObjectsChanged(t domain.EventType, payload json.RawMessage) {
	objects, err := l.cfg.Store.ListObjects(l.pursuit.ID)
	if err == nil {
		l.ctx.Apply(func(c *harness.LearningContext) { c.Objects = objects })
	}
	if err := l.bus.Publish(domain.Event{SessionID: l.session.ID, Type: t, Origin: "document", Payload: payload}); err != nil {
		log.Printf("session: publish: %v", err)
	}
	l.directByType(domain.CellDocument)
}

// Close stops everything for this session. Nothing is written afterwards.
func (l *Live) Close() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	close(l.done)
	id, partial := l.streamID, l.streamBuf.String()
	l.streamID = ""
	l.mu.Unlock()
	l.stopEv()
	l.orch.Stop()
	l.rt.Stop()
	l.unsub()
	if id != "" {
		if err := l.cfg.Store.UpdateMessage(id, partial, true); err != nil {
			log.Printf("session: flush on close: %v", err)
		}
	}
}
