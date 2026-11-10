// Package session runs live Deep Sessions and handles continuity between them.
package session

import (
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"deepflow/internal/agents"
	"deepflow/internal/domain"
	"deepflow/internal/harness"
	"deepflow/internal/orchestrator"
	"deepflow/internal/provider"
	"deepflow/internal/store"
)

const busyLease = 20 * time.Second

// consentSetting is the settings key prefix recording the learner's
// acknowledgement that content goes to a remote provider. It's per provider:
// agreeing to send data to one company says nothing about another.
const consentSetting = "remote_inference_consent:"

// ErrConsentRequired blocks model use until the learner acknowledges disclosure.
var ErrConsentRequired = errors.New("consent required before using a remote model")

// Config configures a Manager.
type Config struct {
	Store         *store.Store
	Provider      provider.Provider
	Policy        orchestrator.Policy
	Coalesce      time.Duration
	TickEvery     time.Duration
	MaxConcurrent int
	AgentBudget   int
	TaskTimeout   time.Duration
}

// Manager owns all live sessions.
type Manager struct {
	cfg   Config
	sched *harness.Scheduler
	mu    sync.Mutex
	live  map[string]*Live // by session ID
}

// NewManager creates a Manager.
func NewManager(cfg Config) *Manager {
	if cfg.TickEvery == 0 {
		cfg.TickEvery = 5 * time.Second
	}
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = 3
	}
	if cfg.Policy.MinCells == 0 {
		cfg.Policy = orchestrator.DefaultPolicy()
	}
	return &Manager{cfg: cfg, sched: harness.NewScheduler(cfg.MaxConcurrent), live: map[string]*Live{}}
}

// ConsentNeeded reports whether the learner must acknowledge remote inference.
func (m *Manager) ConsentNeeded() bool {
	if m.cfg.Provider.Name() == "mock" {
		return false
	}
	v, err := m.cfg.Store.GetSetting(consentSetting + m.cfg.Provider.Name())
	return err != nil || v == ""
}

// GrantConsent records the learner's acknowledgement for the active provider.
func (m *Manager) GrantConsent() error {
	return m.cfg.Store.SetSetting(consentSetting+m.cfg.Provider.Name(), time.Now().Format(time.RFC3339))
}

func (m *Manager) liveForPursuit(pursuitID string) *Live {
	for _, l := range m.live {
		if l.pursuit.ID == pursuitID {
			return l
		}
	}
	return nil
}

// Start resumes the pursuit's open session or begins a new one.
func (m *Manager) Start(pursuitID, inquiry string) (*Live, error) {
	if m.ConsentNeeded() {
		return nil, ErrConsentRequired
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if l := m.liveForPursuit(pursuitID); l != nil {
		return l, nil
	}
	p, err := m.cfg.Store.GetPursuit(pursuitID)
	if err != nil {
		return nil, err
	}
	latest, err := m.cfg.Store.LatestSession(pursuitID)
	switch {
	case err == nil && !latest.Ended():
		return m.resume(p, latest)
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return nil, err
	}
	return m.begin(p, inquiry, latest)
}

// Open returns the live session, resuming it if the process restarted. It
// returns false for a session that has ended.
func (m *Manager) Open(sessionID string) (*Live, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l := m.live[sessionID]; l != nil {
		return l, true, nil
	}
	s, err := m.cfg.Store.GetSession(sessionID)
	if err != nil {
		return nil, false, err
	}
	if s.Ended() {
		return nil, false, nil
	}
	if m.ConsentNeeded() {
		return nil, false, ErrConsentRequired
	}
	p, err := m.cfg.Store.GetPursuit(s.PursuitID)
	if err != nil {
		return nil, false, err
	}
	l, err := m.resume(p, s)
	return l, err == nil, err
}

// Get returns a session only if it is already live.
func (m *Manager) Get(sessionID string) (*Live, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.live[sessionID]
	return l, ok
}

// ForCell finds the live session holding a cell.
func (m *Manager) ForCell(cellID string) (*Live, bool) {
	m.mu.Lock()
	lives := make([]*Live, 0, len(m.live))
	for _, l := range m.live {
		lives = append(lives, l)
	}
	m.mu.Unlock()
	for _, l := range lives {
		for _, c := range l.Workspace().Cells {
			if c.ID == cellID {
				return l, true
			}
		}
	}
	return nil, false
}

// ForPursuit returns the pursuit's live session, if any.
func (m *Manager) ForPursuit(pursuitID string) (*Live, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.liveForPursuit(pursuitID)
	return l, l != nil
}

func (m *Manager) seedContext(p domain.Pursuit, inquiry string) (harness.LearningContext, error) {
	c := harness.LearningContext{PursuitTitle: p.Title, Grade: p.Grade, Journey: p.Journey, Inquiry: inquiry}
	objects, err := m.cfg.Store.ListObjects(p.ID)
	if err != nil {
		return c, err
	}
	c.Objects = objects
	for _, o := range objects {
		switch o.Type {
		case domain.ObjHypothesis:
			c.Hypotheses = append(c.Hypotheses, o.Content)
		case domain.ObjQuestion:
			c.OpenQuestions = append(c.OpenQuestions, o.Content)
		case domain.ObjConcept:
			c.Concepts = append(c.Concepts, o.Content)
		}
	}
	evidence, err := m.cfg.Store.ListEvidence(p.ID)
	if err != nil {
		return c, err
	}
	for i := len(evidence) - 1; i >= 0; i-- { // oldest first
		e := evidence[i]
		if e.Tier != domain.TierWeak {
			c.Demonstrated = append(c.Demonstrated, harness.DemonstratedItem{
				Text: e.Concept + ": " + e.Rationale, Tier: string(e.Tier), Uncertainty: e.Uncertainty, EvidenceIDs: []string{e.ID},
			})
		}
	}
	return c, nil
}

func (m *Manager) begin(p domain.Pursuit, inquiry string, prior domain.Session) (*Live, error) {
	if strings.TrimSpace(inquiry) == "" {
		inquiry = prior.Inquiry
	}
	if strings.TrimSpace(inquiry) == "" {
		inquiry = p.Description
	}
	s, err := m.cfg.Store.CreateSession(p.ID, inquiry)
	if err != nil {
		return nil, err
	}
	seed, err := m.seedContext(p, inquiry)
	if err != nil {
		return nil, err
	}
	if err := m.cfg.Store.TouchPursuit(p.ID); err != nil {
		return nil, err
	}
	l := newLive(m.cfg, s, p, seed, m.sched)
	m.live[s.ID] = l
	l.orch.Init(orchestrator.InitialCellsFor(inquiry, wantsMap(p, inquiry)))
	if prior.ID != "" {
		go l.prepareReturn(prior)
	}
	return l, nil
}

func (m *Manager) resume(p domain.Pursuit, s domain.Session) (*Live, error) {
	seed, err := m.seedContext(p, s.Inquiry)
	if err != nil {
		return nil, err
	}
	msgs, err := m.cfg.Store.ListMessages(s.ID)
	if err != nil {
		return nil, err
	}
	if n := len(msgs); n > 10 {
		msgs = msgs[n-10:]
	}
	for _, msg := range msgs {
		if msg.Role == "user" {
			seed.Recent = append(seed.Recent, harness.Interaction{Kind: string(domain.EvUserMessage), Text: msg.Content})
		}
	}
	l := newLive(m.cfg, s, p, seed, m.sched)
	l.loadRetrieval()
	l.loadDecisions()
	m.live[s.ID] = l
	cells := orchestrator.InitialCellsFor(s.Inquiry, wantsMap(p, s.Inquiry))
	if len(s.Snapshot) > 0 {
		var ws domain.WorkspaceState
		if err := json.Unmarshal(s.Snapshot, &ws); err != nil {
			log.Printf("session: bad snapshot for %s, starting fresh: %v", s.ID, err)
		} else if len(ws.Cells) > 0 && allKnown(ws.Cells) {
			cells = ws.Cells
			for i := range cells {
				cells[i].AgentInstanceID = domain.NewID("agent")
				cells[i].Busy, cells[i].BusyUntil = false, time.Time{}
				if len(cells[i].Content) > 0 && !agents.ValidContent(cells[i].Type, cells[i].Content) {
					cells[i].Content, cells[i].LocalState = nil, nil // rebuilt by the agent
				}
			}
			l.orch.RestorePending(ws.Pending)
		}
	}
	l.orch.Init(cells)
	return l, nil
}

func wantsMap(p domain.Pursuit, inquiry string) bool {
	return IsMapSubject(p.Title, inquiry) || (p.Journey != nil && p.Journey.UsesMaps)
}

// allKnown reports whether every cell's type still exists. A snapshot from an
// earlier version can hold retired card types, and then starts fresh.
func allKnown(cells []domain.Cell) bool {
	for _, c := range cells {
		if !domain.KnownCellType(c.Type) {
			return false
		}
	}
	return true
}

// Shutdown stops every live session.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	lives := make([]*Live, 0, len(m.live))
	for id, l := range m.live {
		lives = append(lives, l)
		delete(m.live, id)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, l := range lives {
		wg.Add(1)
		go func(l *Live) {
			defer wg.Done()
			l.Close()
		}(l)
	}
	wg.Wait()
}
