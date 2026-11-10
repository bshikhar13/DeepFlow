package harness

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"deepflow/internal/domain"
	"deepflow/internal/provider"
)

// RuntimeConfig configures agent execution for one session.
type RuntimeConfig struct {
	SessionID       string
	Provider        provider.Provider
	Scheduler       *Scheduler // created from MaxConcurrent when nil
	MaxConcurrent   int
	AgentBudget     int
	TaskTimeout     time.Duration
	DialogueTimeout time.Duration
	History         func() []provider.Turn
	Metrics         *Metrics
}

type jobKind int

const (
	jobInit jobKind = iota
	jobEvent
	jobDirective
)

type job struct {
	kind      jobKind
	event     domain.Event
	directive Directive
}

// liveCell is the runtime state of one cell's agent instance. mu guards every
// field except inbox.
type liveCell struct {
	cell     domain.Cell
	agent    Agent
	inbox    chan job
	done     chan struct{}
	retired  bool
	suspend  bool
	cancel   context.CancelFunc
	userStop bool
	failed   *job
	budget   *atomic.Int64
}

// Runtime schedules Cell Agents. It is the only path from an agent to the
// workspace, which is how retirement and stale-result rules are enforced.
type Runtime struct {
	cfg     RuntimeConfig
	bus     *Bus
	ctx     *ContextStore
	factory AgentFactory
	sink    Sink
	sched   *Scheduler
	mu      sync.Mutex
	cells   map[string]*liveCell
	stopped bool
	loops   sync.WaitGroup
}

// NewRuntime creates a runtime.
func NewRuntime(cfg RuntimeConfig, b *Bus, c *ContextStore, f AgentFactory, sink Sink) *Runtime {
	if cfg.Scheduler == nil {
		n := cfg.MaxConcurrent
		if n <= 0 {
			n = 3
		}
		cfg.Scheduler = NewScheduler(n)
	}
	if cfg.AgentBudget <= 0 {
		cfg.AgentBudget = 40
	}
	if cfg.TaskTimeout <= 0 {
		// Slower models can need well over 90 seconds for a game or quiz.
		cfg.TaskTimeout = 3 * time.Minute
	}
	if cfg.DialogueTimeout <= 0 {
		cfg.DialogueTimeout = 5 * time.Minute
	}
	if cfg.Metrics == nil {
		cfg.Metrics = &Metrics{}
	}
	return &Runtime{cfg: cfg, bus: b, ctx: c, factory: f, sink: sink, sched: cfg.Scheduler, cells: map[string]*liveCell{}}
}

// Scheduler returns the shared model-call scheduler.
func (r *Runtime) Scheduler() *Scheduler { return r.sched }

// Metrics returns the session's inference counters.
func (r *Runtime) Metrics() *Metrics { return r.cfg.Metrics }

// Start creates the cell's agent instance. A cell with saved content is
// restored as-is; a new one runs Init.
func (r *Runtime) Start(cell domain.Cell) {
	budget := &atomic.Int64{}
	budget.Store(int64(r.cfg.AgentBudget))
	lc := &liveCell{cell: cell, agent: r.factory(cell.Type), inbox: make(chan job, 64), done: make(chan struct{}), budget: budget}
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	if old := r.cells[cell.ID]; old != nil {
		r.mu.Unlock()
		return
	}
	r.cells[cell.ID] = lc
	r.mu.Unlock()
	r.loops.Add(1)
	go func() {
		defer r.loops.Done()
		r.loop(lc)
	}()
	if len(cell.Content) == 0 {
		r.setStatus(lc, domain.StatusInitializing)
		lc.inbox <- job{kind: jobInit}
	} else {
		r.setStatus(lc, domain.StatusWaiting)
	}
}

func (r *Runtime) setStatus(lc *liveCell, s domain.CellStatus) {
	r.mu.Lock()
	if lc.retired {
		r.mu.Unlock()
		return
	}
	lc.cell.Status = s
	id := lc.cell.ID
	r.mu.Unlock()
	r.sink.CellStatus(id, s)
}

func (r *Runtime) get(id string) *liveCell {
	r.mu.Lock()
	defer r.mu.Unlock()
	lc := r.cells[id]
	if lc == nil || lc.retired || r.stopped {
		return nil
	}
	return lc
}

func (r *Runtime) enqueue(id string, j job) {
	lc := r.get(id)
	if lc == nil {
		return
	}
	r.mu.Lock()
	suspended := lc.suspend
	r.mu.Unlock()
	if suspended {
		return
	}
	select {
	case lc.inbox <- j:
	case <-lc.done:
	}
}

// Dispatch sends an event to a cell's agent.
func (r *Runtime) Dispatch(cellID string, ev domain.Event) {
	r.enqueue(cellID, job{kind: jobEvent, event: ev})
}

// Direct sends an Orchestrator directive to a cell's agent.
func (r *Runtime) Direct(cellID string, d Directive) {
	r.enqueue(cellID, job{kind: jobDirective, directive: d})
}

// Retry replays the work that last failed.
func (r *Runtime) Retry(cellID string) {
	lc := r.get(cellID)
	if lc == nil {
		return
	}
	r.mu.Lock()
	failed := lc.failed
	lc.failed = nil
	r.mu.Unlock()
	if failed == nil {
		return
	}
	r.enqueue(cellID, *failed)
}

// BumpUserRevision records a learner edit. Any task already running for the
// cell can no longer overwrite it.
func (r *Runtime) BumpUserRevision(cellID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if lc := r.cells[cellID]; lc != nil {
		lc.cell.UserRevision++
	}
}

// CancelCurrent stops the running task at the learner's request. It is not a failure.
func (r *Runtime) CancelCurrent(cellID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if lc := r.cells[cellID]; lc != nil && lc.cancel != nil {
		lc.userStop = true
		lc.cancel()
	}
}

// SetSuspended pauses or resumes a cell's agent. Suspended agents take no work.
func (r *Runtime) SetSuspended(cellID string, suspended bool) {
	lc := r.get(cellID)
	if lc == nil {
		return
	}
	r.mu.Lock()
	lc.suspend = suspended
	r.mu.Unlock()
	if suspended {
		r.setStatus(lc, domain.StatusSuspended)
	} else {
		r.setStatus(lc, domain.StatusWaiting)
	}
}

// Retire stops a cell's agent and returns what should be preserved. Results
// from the instance are dropped from this moment on.
func (r *Runtime) Retire(cellID string) []domain.Artifact {
	r.mu.Lock()
	lc := r.cells[cellID]
	if lc == nil || lc.retired {
		r.mu.Unlock()
		return nil
	}
	lc.retired = true
	lc.cell.Status = domain.StatusRetiring
	if lc.cancel != nil {
		lc.cancel()
	}
	close(lc.done)
	content, state := lc.cell.Content, lc.cell.LocalState
	cell := lc.cell
	delete(r.cells, cellID)
	r.mu.Unlock()
	arts := lc.agent.Retain(content, state)
	for i := range arts {
		arts[i].SessionID = r.cfg.SessionID
		arts[i].CellID = cell.ID
		arts[i].CellType = cell.Type
	}
	return arts
}

// Stop retires every agent without preserving anything further, and waits
// for running tasks to end so nothing is written afterwards.
func (r *Runtime) Stop() {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.stopped = true
	for _, lc := range r.cells {
		if !lc.retired {
			lc.retired = true
			if lc.cancel != nil {
				lc.cancel()
			}
			close(lc.done)
		}
	}
	r.mu.Unlock()
	finished := make(chan struct{})
	go func() {
		r.loops.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		log.Printf("runtime: an agent ignored cancellation at shutdown")
	}
}

func (r *Runtime) loop(lc *liveCell) {
	for {
		select {
		case <-lc.done:
			return
		case j := <-lc.inbox:
			r.run(lc, j)
		}
	}
}

func (r *Runtime) run(lc *liveCell, j job) {
	timeout := r.cfg.TaskTimeout
	if lc.cell.Type == domain.CellDialogue {
		timeout = r.cfg.DialogueTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	r.mu.Lock()
	if lc.retired {
		r.mu.Unlock()
		return
	}
	lc.cancel = cancel
	lc.userStop = false
	startRev := lc.cell.UserRevision
	if j.kind != jobInit {
		lc.cell.Status = domain.StatusUpdating
	}
	cell := lc.cell
	id := cell.ID
	r.mu.Unlock()
	if j.kind != jobInit {
		r.sink.CellStatus(id, domain.StatusUpdating)
	}

	in := AgentInput{Cell: cell, Context: r.ctx.Project(cell.Type), Delta: func(text string) {
		r.mu.Lock()
		dead := lc.retired
		r.mu.Unlock()
		if !dead {
			r.sink.Delta(id, text)
		}
	}}
	g := &Governed{P: r.cfg.Provider, Sched: r.sched, Metrics: r.cfg.Metrics}
	if cell.Type == domain.CellDialogue {
		g.High = true
		if r.cfg.History != nil {
			in.History = r.cfg.History()
		}
	} else {
		g.Budget = lc.budget
	}
	in.Provider = g

	out, err := r.safeCall(ctx, lc, in, j)

	r.mu.Lock()
	if lc.retired {
		r.mu.Unlock()
		return
	}
	lc.cancel = nil
	stale := lc.cell.UserRevision != startRev
	stopped := lc.userStop
	var snapshot domain.Cell
	publish := false
	switch {
	case stopped:
		lc.cell.Status = domain.StatusWaiting
		lc.cell.Error = ""
	case stale:
		lc.cell.Status = domain.StatusWaiting
	case errors.Is(err, ErrBudgetExhausted):
		lc.cell.Status = domain.StatusWaiting
		lc.cell.Error = "This cell has used its model budget for the session, so it can't update further."
	case err != nil:
		lc.cell.Status = domain.StatusFailed
		lc.cell.Error = userMessage(err)
		jc := j
		lc.failed = &jc
		log.Printf("cell %s (%s): %v", id, cell.Type, err)
	default:
		if out.Content != nil {
			lc.cell.Content = out.Content
		}
		if out.LocalState != nil {
			lc.cell.LocalState = out.LocalState
		}
		lc.cell.Status = domain.StatusWaiting
		lc.cell.Error = ""
		lc.cell.ContextVersion = in.Context.Version
		publish = true
	}
	if lc.suspend {
		lc.cell.Status = domain.StatusSuspended
	}
	snapshot = lc.cell
	r.mu.Unlock()

	if stale || stopped {
		r.sink.CellStatus(id, snapshot.Status)
		return
	}
	if !publish {
		r.sink.CellUpdated(snapshot)
		return
	}
	for _, e := range out.Publish {
		e.SessionID, e.Origin = r.cfg.SessionID, id
		e.ContextVersion = in.Context.Version
		if err := r.bus.Publish(e); err != nil {
			log.Printf("cell %s: publish %s: %v", id, e.Type, err)
		}
	}
	r.sink.CellUpdated(snapshot)
	for _, q := range out.Requests {
		r.sink.Request(id, q)
	}
	if out.Then != nil {
		r.Direct(id, *out.Then)
	}
}

func (r *Runtime) safeCall(ctx context.Context, lc *liveCell, in AgentInput, j job) (out AgentOutput, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("agent panic: %v", p)
		}
	}()
	switch j.kind {
	case jobInit:
		return lc.agent.Init(ctx, in)
	case jobDirective:
		return lc.agent.Update(ctx, in, j.directive)
	}
	return lc.agent.Handle(ctx, in, j.event)
}

func userMessage(err error) string {
	switch {
	case errors.Is(err, provider.ErrRefused):
		return "The model declined this request. Try rewording, or refresh the cell."
	case errors.Is(err, provider.ErrNoRoute):
		return "This AI model isn't available under DeepFlow's student privacy setting. Try another model."
	case errors.Is(err, provider.ErrAccount):
		return "The AI service turned this down. Check the API key and that it has credit."
	case errors.Is(err, provider.ErrMalformed), errors.Is(err, provider.ErrTruncated):
		return "The model returned something unusable. Retrying usually works."
	case errors.Is(err, context.DeadlineExceeded):
		return "This took too long and was stopped."
	}
	return "Something went wrong in this cell. Your other cells are unaffected."
}
