package orchestrator

import (
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"deepflow/internal/domain"
	"deepflow/internal/evaluator"
	"deepflow/internal/harness"
	"deepflow/internal/store"
)

var (
	// ErrProtected is returned when an action would remove the primary dialogue.
	ErrProtected = errors.New("the dialogue cell can't be removed")
	// ErrNothingToUndo is returned when no structural change can be reversed.
	ErrNothingToUndo = errors.New("nothing to undo")
	// ErrNoCell is returned for an unknown cell.
	ErrNoCell = errors.New("no such cell")
)

// Engine is the part of the runtime the Orchestrator controls.
type Engine interface {
	Start(domain.Cell)
	Direct(cellID string, d harness.Directive)
	Retire(cellID string) []domain.Artifact
	BumpUserRevision(cellID string)
}

// Notifier tells the renderer what changed.
type Notifier interface {
	WorkspaceChanged(domain.WorkspaceState)
	CellChanged(domain.Cell)
	DecisionMade(d domain.Decision, canUndo bool)
}

// Config wires an Orchestrator to its session.
type Config struct {
	SessionID string
	Inquiry   string
	Policy    Policy
	Engine    Engine
	Store     *store.Store
	Bus       *harness.Bus
	Context   *harness.ContextStore
	Notifier  Notifier
	Now       func() time.Time
}

type undoEntry struct {
	before  []domain.Cell
	removed []domain.Cell // cells to bring back
	added   []string      // cell IDs to retire
}

// Orchestrator owns workspace composition for one session.
type Orchestrator struct {
	cfg            Config
	mu             sync.Mutex
	persistMu      sync.Mutex
	ws             domain.WorkspaceState
	pending        []domain.Decision
	lastStructural time.Time
	lastUpdate     map[string]time.Time
	undo           []undoEntry
	removedTypes   map[domain.CellType]bool
	retiredDrafts  map[string]string
	requested      map[string]time.Time
	stopped        bool
}

// New creates an Orchestrator.
func New(cfg Config) *Orchestrator {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Orchestrator{cfg: cfg, ws: domain.WorkspaceState{SessionID: cfg.SessionID}, lastUpdate: map[string]time.Time{}, removedTypes: map[domain.CellType]bool{}, retiredDrafts: map[string]string{}, requested: map[string]time.Time{}}
}

// RestorePending reinstates decisions that were deferred before a restart.
// They go through Decide again on the next Tick.
func (o *Orchestrator) RestorePending(p []domain.Decision) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pending = p
}

// Init installs cells (new or restored) and starts their agents.
func (o *Orchestrator) Init(cells []domain.Cell) {
	o.mu.Lock()
	o.ws.Cells = cells
	o.ws.Revision++
	o.refreshResponsibilities()
	o.mu.Unlock()
	o.persist()
	for _, c := range cells {
		o.cfg.Engine.Start(c)
	}
}

// Stop prevents any further structural activity.
func (o *Orchestrator) Stop() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopped = true
}

func (o *Orchestrator) copyWS() domain.WorkspaceState {
	ws := o.ws
	ws.Cells = append([]domain.Cell(nil), o.ws.Cells...)
	ws.Pending = append([]domain.Decision(nil), o.pending...)
	return ws
}

// Snapshot returns the current workspace state.
func (o *Orchestrator) Snapshot() domain.WorkspaceState {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.copyWS()
}

// persist saves the latest state. It reads the state inside persistMu, so
// concurrent saves can't leave an older snapshot on disk.
func (o *Orchestrator) persist() {
	if o.cfg.Store == nil {
		return
	}
	o.persistMu.Lock()
	defer o.persistMu.Unlock()
	if err := o.cfg.Store.SaveSnapshot(o.cfg.SessionID, o.Snapshot()); err != nil {
		log.Printf("orchestrator: snapshot: %v", err)
	}
}

func (o *Orchestrator) cellIndex(id string) int {
	for i, c := range o.ws.Cells {
		if c.ID == id {
			return i
		}
	}
	return -1
}

func (o *Orchestrator) refreshResponsibilities() {
	if o.cfg.Context == nil {
		return
	}
	list := make([]harness.CellResponsibility, 0, len(o.ws.Cells))
	for _, c := range o.ws.Cells {
		list = append(list, harness.CellResponsibility{ID: c.ID, Type: c.Type, Objective: c.Objective})
	}
	o.cfg.Context.Apply(func(l *harness.LearningContext) { l.Cells = list })
}

// MergeCell records an agent's new content and status.
func (o *Orchestrator) MergeCell(c domain.Cell) {
	o.mu.Lock()
	i := o.cellIndex(c.ID)
	if i < 0 {
		o.mu.Unlock()
		return
	}
	cur := &o.ws.Cells[i]
	cur.Content, cur.LocalState, cur.Status, cur.Error = c.Content, c.LocalState, c.Status, c.Error
	cur.ContextVersion, cur.UserRevision = c.ContextVersion, c.UserRevision
	merged := *cur
	o.mu.Unlock()
	o.persist()
	o.cfg.Notifier.CellChanged(merged)
}

// SetStatus records a lifecycle change.
func (o *Orchestrator) SetStatus(id string, s domain.CellStatus) {
	o.mu.Lock()
	i := o.cellIndex(id)
	if i < 0 {
		o.mu.Unlock()
		return
	}
	o.ws.Cells[i].Status = s
	c := o.ws.Cells[i]
	o.mu.Unlock()
	o.cfg.Notifier.CellChanged(c)
}

// Handle processes Evaluator recommendations.
func (o *Orchestrator) Handle(recs []evaluator.Recommendation) {
	o.decideAndApply(recs)
}

// Request converts a Cell Agent's request into a recommendation. Agents can
// only ask; this is the single place such a request can have effect.
func (o *Orchestrator) Request(cellID string, r harness.CellRequest) {
	rec := evaluator.Recommendation{CellID: cellID, CellType: r.CellType, Objective: r.Objective, Reason: r.Reason}
	switch r.Kind {
	case "replace":
		rec.Action = domain.ActReplace
	case "retire":
		rec.Action = domain.ActRetire
	case "spawn":
		rec.Action, rec.CellID = domain.ActSpawn, ""
	default:
		return
	}
	o.decideAndApply([]evaluator.Recommendation{rec})
}

// Tick expires busy leases and retries deferred decisions.
func (o *Orchestrator) Tick() {
	o.decideAndApply(nil)
}

func decisionToRec(d domain.Decision) evaluator.Recommendation {
	return evaluator.Recommendation{Action: d.Action, CellID: d.CellID, CellType: d.CellType, Objective: d.Objective, Reason: d.Reason}
}

func (o *Orchestrator) decideAndApply(recs []evaluator.Recommendation) {
	o.mu.Lock()
	if o.stopped {
		o.mu.Unlock()
		return
	}
	now := o.cfg.Now()
	changedBusy := []domain.Cell{}
	for i := range o.ws.Cells {
		c := &o.ws.Cells[i]
		if c.Busy && !c.BusyUntil.After(now) {
			c.Busy = false
			changedBusy = append(changedBusy, *c)
		}
	}
	all := make([]evaluator.Recommendation, 0, len(o.pending)+len(recs))
	for _, d := range o.pending {
		all = append(all, decisionToRec(d))
	}
	all = append(all, o.withoutRequested(recs, now)...)
	if len(all) == 0 {
		o.mu.Unlock()
		for _, c := range changedBusy {
			o.cfg.Notifier.CellChanged(c)
		}
		return
	}
	apply, pending := Decide(o.copyWS(), all, o.lastStructural, o.lastUpdate, now, o.cfg.Policy)
	o.pending = pending

	var effects []func()
	for _, d := range apply {
		effects = append(effects, o.applyLocked(d, now)...)
	}
	o.mu.Unlock()

	for _, c := range changedBusy {
		o.cfg.Notifier.CellChanged(c)
	}
	for _, f := range effects {
		f()
	}
	if len(apply) > 0 {
		o.persist()
	}
}

func directiveKind(a domain.Action) string {
	switch a {
	case domain.ActDeepen:
		return "deepen"
	case domain.ActSimplify:
		return "simplify"
	}
	return "update"
}

// retireEffect stops a cell's agent and keeps what it produced. Any draft the
// learner left in the cell becomes an artifact, and is remembered so that
// undoing the change can put it back in the box.
func (o *Orchestrator) retireEffect(id string, cellType domain.CellType) func() {
	return func() {
		for _, a := range o.cfg.Engine.Retire(id) {
			art := a
			if o.cfg.Store == nil {
				continue
			}
			if err := o.cfg.Store.AddArtifact(&art); err != nil {
				log.Printf("orchestrator: artifact: %v", err)
			}
		}
		if o.cfg.Store == nil {
			return
		}
		drafts, err := o.cfg.Store.GetDrafts(o.cfg.SessionID)
		if err != nil {
			log.Printf("orchestrator: drafts: %v", err)
			return
		}
		draft := drafts[id]
		if draft == "" {
			return
		}
		b, _ := json.Marshal(map[string]string{"draft": draft})
		if err := o.cfg.Store.AddArtifact(&domain.Artifact{SessionID: o.cfg.SessionID, CellID: id, CellType: cellType, Content: b, LearnerAuthored: true}); err != nil {
			log.Printf("orchestrator: draft artifact: %v", err)
			return // keep the draft row rather than lose the text
		}
		if err := o.cfg.Store.SaveDraft(o.cfg.SessionID, id, ""); err != nil {
			log.Printf("orchestrator: clear draft: %v", err)
		}
		o.mu.Lock()
		o.retiredDrafts[id] = draft
		o.mu.Unlock()
	}
}

func (o *Orchestrator) announce(d domain.Decision, canUndo bool, ws *domain.WorkspaceState) []func() {
	var fx []func()
	if ws != nil {
		w := *ws
		fx = append(fx, func() { o.cfg.Notifier.WorkspaceChanged(w) })
	}
	fx = append(fx, func() {
		if o.cfg.Bus != nil {
			b, _ := json.Marshal(d)
			if err := o.cfg.Bus.Publish(domain.Event{SessionID: o.cfg.SessionID, Type: domain.EvOrchestratorDecide, Origin: "orchestrator", Payload: b}); err != nil {
				log.Printf("orchestrator: publish: %v", err)
			}
		}
		o.cfg.Notifier.DecisionMade(d, canUndo)
	})
	return fx
}

// applyLocked mutates the workspace and returns the side effects to run after
// the lock is released, because runtime calls re-enter through the Sink.
func (o *Orchestrator) applyLocked(d domain.Decision, now time.Time) []func() {
	var fx []func()
	switch d.Action {
	case domain.ActKeep:
		return nil

	case domain.ActUpdate, domain.ActDeepen, domain.ActSimplify:
		i := o.cellIndex(d.CellID)
		if i < 0 {
			return nil
		}
		if d.Objective != "" {
			o.ws.Cells[i].Objective = d.Objective
		}
		o.lastUpdate[d.CellID] = now
		o.refreshResponsibilities()
		id, objective := d.CellID, o.ws.Cells[i].Objective
		kind := directiveKind(d.Action)
		fx = append(fx, func() { o.cfg.Engine.Direct(id, harness.Directive{Kind: kind, Objective: objective}) })
		return append(fx, o.announce(d, false, nil)...)

	case domain.ActSpawn:
		before := append([]domain.Cell(nil), o.ws.Cells...)
		c := newCell(d.CellType, d.Objective)
		o.ws.Cells = Reflow(append(o.ws.Cells, c))
		o.finishStructural(now, undoEntry{before: before, added: []string{c.ID}})
		ws := o.copyWS()
		var placed domain.Cell
		for _, x := range ws.Cells {
			if x.ID == c.ID {
				placed = x
			}
		}
		fx = append(fx, func() { o.cfg.Engine.Start(placed) })
		return append(fx, o.announce(d, true, &ws)...)

	case domain.ActReplace:
		i := o.cellIndex(d.CellID)
		if i < 0 {
			return nil
		}
		before := append([]domain.Cell(nil), o.ws.Cells...)
		old := o.ws.Cells[i]
		c := newCell(d.CellType, d.Objective)
		c.Layout = old.Layout
		o.ws.Cells[i] = c
		o.finishStructural(now, undoEntry{before: before, removed: []domain.Cell{old}, added: []string{c.ID}})
		ws := o.copyWS()
		fx = append(fx, o.retireEffect(old.ID, old.Type), func() { o.cfg.Engine.Start(c) })
		return append(fx, o.announce(d, true, &ws)...)

	case domain.ActRetire:
		i := o.cellIndex(d.CellID)
		if i < 0 {
			return nil
		}
		before := append([]domain.Cell(nil), o.ws.Cells...)
		old := o.ws.Cells[i]
		o.ws.Cells = Reflow(append(append([]domain.Cell(nil), o.ws.Cells[:i]...), o.ws.Cells[i+1:]...))
		o.finishStructural(now, undoEntry{before: before, removed: []domain.Cell{old}})
		ws := o.copyWS()
		fx = append(fx, o.retireEffect(old.ID, old.Type))
		return append(fx, o.announce(d, true, &ws)...)
	}
	return nil
}

func (o *Orchestrator) finishStructural(now time.Time, u undoEntry) {
	o.lastStructural = now
	o.undo = append(o.undo, u)
	o.ws.Revision++
	o.refreshResponsibilities()
}

// UserPin protects a cell from automatic removal and clears deferred changes to it.
func (o *Orchestrator) UserPin(id string) error { return o.setPinned(id, true) }

// UserUnpin removes the protection.
func (o *Orchestrator) UserUnpin(id string) error { return o.setPinned(id, false) }

func (o *Orchestrator) setPinned(id string, pinned bool) error {
	o.mu.Lock()
	i := o.cellIndex(id)
	if i < 0 {
		o.mu.Unlock()
		return ErrNoCell
	}
	o.ws.Cells[i].Pinned = pinned
	if pinned {
		kept := o.pending[:0]
		for _, d := range o.pending {
			if d.CellID == id && d.Action.IsStructural() {
				continue
			}
			kept = append(kept, d)
		}
		o.pending = kept
	}
	c := o.ws.Cells[i]
	o.mu.Unlock()
	o.persist()
	o.cfg.Notifier.CellChanged(c)
	return nil
}

// SetBusy marks a cell as being actively used for the lease duration.
func (o *Orchestrator) SetBusy(id string, lease time.Duration) error {
	o.mu.Lock()
	i := o.cellIndex(id)
	if i < 0 {
		o.mu.Unlock()
		return ErrNoCell
	}
	c := &o.ws.Cells[i]
	wasBusy := c.Busy
	c.BusyUntil = o.cfg.Now().Add(lease)
	c.Busy = lease > 0
	copyC := *c
	o.mu.Unlock()
	if wasBusy != copyC.Busy {
		o.cfg.Notifier.CellChanged(copyC)
	}
	return nil
}

// UserRefresh asks a cell's agent for an alternative. It is an explicit
// request, so the content cooldown doesn't apply.
func (o *Orchestrator) UserRefresh(id string) error {
	o.mu.Lock()
	i := o.cellIndex(id)
	if i < 0 {
		o.mu.Unlock()
		return ErrNoCell
	}
	c := o.ws.Cells[i]
	o.lastUpdate[id] = o.cfg.Now()
	o.mu.Unlock()
	o.cfg.Engine.BumpUserRevision(id)
	o.cfg.Engine.Direct(id, harness.Directive{Kind: "refresh", Objective: c.Objective})
	return nil
}

// UserRemove retires a cell at the learner's request, immediately. If that
// takes the workspace below its minimum, a replacement of a type the learner
// hasn't removed is added at once.
func (o *Orchestrator) UserRemove(id string) error {
	o.mu.Lock()
	i := o.cellIndex(id)
	if i < 0 {
		o.mu.Unlock()
		return ErrNoCell
	}
	old := o.ws.Cells[i]
	if old.Type == domain.CellDialogue {
		o.mu.Unlock()
		return ErrProtected
	}
	before := append([]domain.Cell(nil), o.ws.Cells...)
	o.removedTypes[old.Type] = true
	o.ws.Cells = append(append([]domain.Cell(nil), o.ws.Cells[:i]...), o.ws.Cells[i+1:]...)
	kept := o.pending[:0]
	for _, d := range o.pending {
		if d.CellID != id {
			kept = append(kept, d)
		}
	}
	o.pending = kept
	entry := undoEntry{before: before, removed: []domain.Cell{old}}
	var added *domain.Cell
	if len(o.ws.Cells) < o.cfg.Policy.MinCells {
		c := newCell(o.replacementType(), "Try another angle on: "+o.cfg.Inquiry)
		o.ws.Cells = append(o.ws.Cells, c)
		entry.added = []string{c.ID}
		added = &c
	}
	o.ws.Cells = Reflow(o.ws.Cells)
	o.ws.Revision++
	o.undo = append(o.undo, entry)
	o.refreshResponsibilities()
	if added != nil {
		for _, c := range o.ws.Cells {
			if c.ID == added.ID {
				*added = c
			}
		}
	}
	ws := o.copyWS()
	d := domain.Decision{Action: domain.ActRetire, CellID: id, Reason: "You removed this cell."}
	o.mu.Unlock()

	o.retireEffect(id, old.Type)()
	if added != nil {
		o.cfg.Engine.Start(*added)
	}
	o.persist()
	for _, f := range o.announce(d, true, &ws) {
		f()
	}
	return nil
}

func (o *Orchestrator) replacementType() domain.CellType {
	present := map[domain.CellType]bool{}
	for _, c := range o.ws.Cells {
		present[c.Type] = true
	}
	catalog := []domain.CellType{domain.CellThought, domain.CellQuiz, domain.CellGame, domain.CellConcept, domain.CellJourney, domain.CellDocument}
	for _, t := range catalog {
		if !present[t] && !o.removedTypes[t] {
			return t
		}
	}
	for _, t := range catalog {
		if !o.removedTypes[t] {
			return t
		}
	}
	return domain.CellThought
}

// Undo reverses the most recent structural change.
func (o *Orchestrator) Undo() error {
	o.mu.Lock()
	if len(o.undo) == 0 {
		o.mu.Unlock()
		return ErrNothingToUndo
	}
	u := o.undo[len(o.undo)-1]
	o.undo = o.undo[:len(o.undo)-1]
	addedTypes := map[string]domain.CellType{}
	for _, id := range u.added {
		if i := o.cellIndex(id); i >= 0 {
			addedTypes[id] = o.ws.Cells[i].Type
		}
	}
	restoreDrafts := map[string]string{}
	for _, c := range u.removed {
		if d, ok := o.retiredDrafts[c.ID]; ok {
			restoreDrafts[c.ID] = d
			delete(o.retiredDrafts, c.ID)
		}
	}

	restored := map[string]domain.Cell{}
	for _, c := range u.removed {
		c.AgentInstanceID = domain.NewID("agent")
		c.Status = domain.StatusWaiting
		delete(o.removedTypes, c.Type)
		restored[c.ID] = c
	}
	cells := make([]domain.Cell, 0, len(u.before))
	for _, b := range u.before {
		if r, ok := restored[b.ID]; ok {
			cells = append(cells, r)
			continue
		}
		if i := o.cellIndex(b.ID); i >= 0 {
			cur := o.ws.Cells[i]
			cur.Layout = b.Layout
			cells = append(cells, cur)
		}
	}
	o.ws.Cells = cells
	o.ws.Revision++
	o.refreshResponsibilities()
	ws := o.copyWS()
	o.mu.Unlock()

	for _, id := range u.added {
		o.retireEffect(id, addedTypes[id])()
	}
	for id, d := range restoreDrafts {
		if o.cfg.Store != nil {
			if err := o.cfg.Store.SaveDraft(o.cfg.SessionID, id, d); err != nil {
				log.Printf("orchestrator: restore draft: %v", err)
			}
		}
	}
	for _, c := range restored {
		o.cfg.Engine.Start(c)
	}
	o.persist()
	d := domain.Decision{Action: domain.ActKeep, Reason: "You undid the last workspace change."}
	for _, f := range o.announce(d, false, &ws) {
		f()
	}
	return nil
}

// requestProtection is how long a card the student asked for is safe from
// automatic changes, so a recommendation can't undo what they just requested.
const requestProtection = 2 * time.Minute

// UserRequest acts at once on a student asking for a card. It's an explicit user
// action, so it isn't subject to the adaptation window or the content cooldown.
func (o *Orchestrator) UserRequest(t domain.CellType, objective, reason string) {
	o.mu.Lock()
	if o.stopped || !domain.KnownCellType(t) {
		o.mu.Unlock()
		return
	}
	now := o.cfg.Now()
	idx := -1
	for i, c := range o.ws.Cells {
		if c.Type == t {
			idx = i
			break
		}
	}
	var effects []func()
	if idx < 0 {
		if len(o.ws.Cells) >= o.cfg.Policy.MaxCells {
			o.mu.Unlock()
			return
		}
		delete(o.removedTypes, t)
		effects = o.applyLocked(domain.Decision{Action: domain.ActSpawn, CellType: t, Objective: objective, Reason: reason}, now)
		for _, c := range o.ws.Cells {
			if c.Type == t {
				o.requested[c.ID] = now
			}
		}
	} else {
		cell := &o.ws.Cells[idx]
		cell.Objective = objective
		id := cell.ID
		o.lastUpdate[id] = now
		o.requested[id] = now
		kept := o.pending[:0]
		for _, d := range o.pending {
			if d.CellID != id {
				kept = append(kept, d)
			}
		}
		o.pending = kept
		o.refreshResponsibilities()
		effects = append(effects, func() {
			o.cfg.Engine.BumpUserRevision(id)
			o.cfg.Engine.Direct(id, harness.Directive{Kind: "refresh", Objective: objective})
		})
		effects = append(effects, o.announce(domain.Decision{Action: domain.ActUpdate, CellID: id, CellType: t, Objective: objective, Reason: reason}, false, nil)...)
	}
	o.mu.Unlock()
	for _, f := range effects {
		f()
	}
	o.persist()
}

// withoutRequested drops new recommendations for cards the student just asked for.
func (o *Orchestrator) withoutRequested(recs []evaluator.Recommendation, now time.Time) []evaluator.Recommendation {
	if len(o.requested) == 0 {
		return recs
	}
	kept := make([]evaluator.Recommendation, 0, len(recs))
	for _, r := range recs {
		if c, ok := resolve(o.ws, r); ok {
			if at, asked := o.requested[c.ID]; asked && now.Sub(at) < requestProtection {
				continue
			}
		}
		kept = append(kept, r)
	}
	return kept
}
