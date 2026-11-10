package harness

import (
	"encoding/json"
	"sync"

	"deepflow/internal/domain"
)

// Interaction is a recent meaningful learner action.
type Interaction struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// DemonstratedItem is one evidence-backed observation of understanding.
type DemonstratedItem struct {
	Text        string   `json:"text"`
	Tier        string   `json:"tier"`
	Uncertainty string   `json:"uncertainty"`
	EvidenceIDs []string `json:"evidenceIds"`
}

// CellResponsibility tells agents what each visible cell is for.
type CellResponsibility struct {
	ID        string          `json:"id"`
	Type      domain.CellType `json:"type"`
	Objective string          `json:"objective"`
}

// LearningContext is the Shared Learning Context for one session.
type LearningContext struct {
	Version        int64                  `json:"version"`
	PursuitTitle   string                 `json:"pursuitTitle"`
	Grade          int                    `json:"grade"`
	Journey        *domain.Journey        `json:"journey,omitempty"`
	Inquiry        string                 `json:"inquiry"`
	Intent         string                 `json:"intent"`
	Recent         []Interaction          `json:"recent"`
	Concepts       []string               `json:"concepts"`
	Hypotheses     []string               `json:"hypotheses"`
	Misconceptions []string               `json:"misconceptions"`
	Gaps           []string               `json:"gaps"`
	OpenQuestions  []string               `json:"openQuestions"`
	Demonstrated   []DemonstratedItem     `json:"demonstrated"`
	Cells          []CellResponsibility   `json:"cells"`
	Objects        []domain.ConceptObject `json:"objects"`
	Readiness      string                 `json:"readiness"`
}

// ContextView is the portion of the context an agent is allowed to see.
type ContextView = LearningContext

// ContextStore holds and versions the shared context.
type ContextStore struct {
	mu sync.RWMutex
	c  LearningContext
}

// NewContextStore returns a store seeded with c.
func NewContextStore(c LearningContext) *ContextStore { return &ContextStore{c: c} }

// Apply mutates the context and returns its new version.
func (s *ContextStore) Apply(fn func(*LearningContext)) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.c)
	s.c.Version++
	return s.c.Version
}

// Snapshot returns a deep copy of the full context.
func (s *ContextStore) Snapshot() LearningContext {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.c)
}

func clone(c LearningContext) LearningContext {
	b, _ := json.Marshal(c)
	var out LearningContext
	_ = json.Unmarshal(b, &out)
	return out
}

// Project returns the role-specific view for a cell type, so agents receive
// only what they need.
func (s *ContextStore) Project(t domain.CellType) ContextView {
	v := s.Snapshot()
	switch t {
	case domain.CellDialogue:
		return v
	case domain.CellConcept, domain.CellQuiz, domain.CellThought:
		if n := len(v.Recent); n > 5 {
			v.Recent = v.Recent[n-5:]
		}
		v.Objects = nil
	case domain.CellGame, domain.CellMap:
		if n := len(v.Recent); n > 5 {
			v.Recent = v.Recent[n-5:]
		}
		v.Objects, v.Demonstrated = nil, nil
	case domain.CellJourney:
		v.Recent, v.Objects = nil, nil
	case domain.CellDocument:
		v.Recent, v.Demonstrated = nil, nil
	}
	v.Cells = nil
	return v
}
