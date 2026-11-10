package domain

import (
	"encoding/json"
	"time"
)

// Pursuit is a long-running exploration of a subject.
type Pursuit struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Grade       int       `json:"grade"` // school class, 6 to 12
	Journey     *Journey  `json:"journey,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Session is a Deep Session within a pursuit.
type Session struct {
	ID                 string          `json:"id"`
	PursuitID          string          `json:"pursuitId"`
	Inquiry            string          `json:"inquiry"`
	StartedAt          time.Time       `json:"startedAt"`
	EndedAt            time.Time       `json:"endedAt"`
	Summary            string          `json:"summary"`
	Snapshot           json.RawMessage `json:"-"`
	RetrievalQuestions json.RawMessage `json:"-"`
	NeedsConsolidation bool            `json:"needsConsolidation"`
}

// Ended reports whether the session has finished.
func (s Session) Ended() bool { return !s.EndedAt.IsZero() }

// Message is one dialogue turn.
type Message struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"sessionId"`
	Role        string    `json:"role"`
	Content     string    `json:"content"`
	Interrupted bool      `json:"interrupted"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ObjectType classifies a Living Document object.
type ObjectType string

const (
	ObjConcept    ObjectType = "concept"
	ObjHypothesis ObjectType = "hypothesis"
	ObjInsight    ObjectType = "insight"
	ObjConnection ObjectType = "connection"
	ObjQuestion   ObjectType = "question"
)

// Authorship records who produced a formulation (PRD section 16.3).
type Authorship string

const (
	AuthorAI        Authorship = "ai"
	AuthorLearner   Authorship = "learner"
	AuthorEdited    Authorship = "edited"
	AuthorReference Authorship = "reference"
)

// ConceptObject is a versioned Living Document object.
type ConceptObject struct {
	ID            string     `json:"id"`
	PursuitID     string     `json:"pursuitId"`
	Type          ObjectType `json:"type"`
	Content       string     `json:"content"`
	RelatedIDs    []string   `json:"relatedIds"`
	Authorship    Authorship `json:"authorship"`
	Confidence    string     `json:"confidence"`
	Status        string     `json:"status"`
	SourceEventID string     `json:"sourceEventId,omitempty"`
	Version       int        `json:"version"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

// ObjectVersion is one historical formulation of an object.
type ObjectVersion struct {
	ObjectID   string     `json:"objectId"`
	Version    int        `json:"version"`
	Content    string     `json:"content"`
	Authorship Authorship `json:"authorship"`
	ChangedAt  time.Time  `json:"changedAt"`
}

// EvidenceTier grades the strength of understanding evidence (PRD 15.2).
type EvidenceTier string

const (
	TierWeak     EvidenceTier = "weak"
	TierModerate EvidenceTier = "moderate"
	TierStrong   EvidenceTier = "strong"
	TierDurable  EvidenceTier = "durable"
)

// Evidence links an understanding assessment to a learner interaction.
type Evidence struct {
	ID            string       `json:"id"`
	PursuitID     string       `json:"pursuitId"`
	SessionID     string       `json:"sessionId"`
	Concept       string       `json:"concept"`
	LearnerAction string       `json:"learnerAction"`
	Tier          EvidenceTier `json:"tier"`
	Correctness   string       `json:"correctness"`
	Reasoning     string       `json:"reasoning"`
	Rationale     string       `json:"rationale"`
	Uncertainty   string       `json:"uncertainty"`
	SourceEventID string       `json:"sourceEventId"`
	CreatedAt     time.Time    `json:"createdAt"`
}

// Artifact preserves a cell's output after retirement.
type Artifact struct {
	ID              string          `json:"id"`
	SessionID       string          `json:"sessionId"`
	CellID          string          `json:"cellId"`
	CellType        CellType        `json:"cellType"`
	Content         json.RawMessage `json:"content"`
	LearnerAuthored bool            `json:"learnerAuthored"`
	CreatedAt       time.Time       `json:"createdAt"`
}
