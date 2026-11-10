// Package domain holds the types shared across DeepFlow's subsystems.
package domain

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

// NewID returns a random identifier with the given prefix.
func NewID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(b)
}

// CellType identifies a learning experience in the cell catalog.
type CellType string

const (
	CellDialogue CellType = "dialogue"
	CellConcept  CellType = "concept"
	CellGame     CellType = "game"
	CellQuiz     CellType = "quiz"
	CellJourney  CellType = "journey"
	CellThought  CellType = "thought"
	CellDocument CellType = "document"
	CellMap      CellType = "map"
)

// KnownCellType reports whether t is in the catalog. Older snapshots and model
// output can name types that no longer exist.
func KnownCellType(t CellType) bool {
	switch t {
	case CellDialogue, CellConcept, CellGame, CellQuiz, CellJourney, CellThought, CellDocument, CellMap:
		return true
	}
	return false
}

// CellStatus is a cell's lifecycle state (PRD section 11).
type CellStatus string

const (
	StatusProposed     CellStatus = "proposed"
	StatusInitializing CellStatus = "initializing"
	StatusActive       CellStatus = "active"
	StatusWaiting      CellStatus = "waiting"
	StatusUpdating     CellStatus = "updating"
	StatusSuspended    CellStatus = "suspended"
	StatusRetiring     CellStatus = "retiring"
	StatusTerminated   CellStatus = "terminated"
	StatusFailed       CellStatus = "failed"
)

// Layout positions a cell on the 12-column grid.
type Layout struct {
	Col     int `json:"col"`
	Row     int `json:"row"`
	ColSpan int `json:"colSpan"`
	RowSpan int `json:"rowSpan"`
}

// Cell is a runtime cell instance.
type Cell struct {
	ID              string          `json:"id"`
	Type            CellType        `json:"type"`
	AgentInstanceID string          `json:"agentInstanceId"`
	Objective       string          `json:"objective"`
	Status          CellStatus      `json:"status"`
	Pinned          bool            `json:"pinned"`
	Busy            bool            `json:"busy"`
	BusyUntil       time.Time       `json:"busyUntil"`
	Layout          Layout          `json:"layout"`
	Content         json.RawMessage `json:"content,omitempty"`
	LocalState      json.RawMessage `json:"localState,omitempty"`
	ContextVersion  int64           `json:"contextVersion"`
	UserRevision    int64           `json:"userRevision"`
	Error           string          `json:"error,omitempty"`
}

// WorkspaceState is the visible composition of a session's workspace.
type WorkspaceState struct {
	SessionID   string     `json:"sessionId"`
	Cells       []Cell     `json:"cells"`
	FocusedCell string     `json:"focusedCell,omitempty"`
	Revision    int64      `json:"revision"`
	Pending     []Decision `json:"pending,omitempty"`
}

// Action is an orchestration decision type (PRD section 8.3).
type Action string

const (
	ActKeep       Action = "KEEP"
	ActUpdate     Action = "UPDATE"
	ActDeepen     Action = "DEEPEN"
	ActSimplify   Action = "SIMPLIFY"
	ActSpawn      Action = "SPAWN"
	ActReplace    Action = "REPLACE"
	ActRetire     Action = "RETIRE"
	ActReposition Action = "REPOSITION"
	ActSuspend    Action = "SUSPEND"
	ActResume     Action = "RESUME"
)

// IsStructural reports whether the action changes workspace composition.
func (a Action) IsStructural() bool {
	switch a {
	case ActSpawn, ActReplace, ActRetire, ActReposition:
		return true
	}
	return false
}

// Decision is one Orchestrator outcome.
type Decision struct {
	Action      Action   `json:"action"`
	CellID      string   `json:"cellId,omitempty"`
	CellType    CellType `json:"cellType,omitempty"`
	Objective   string   `json:"objective,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	Unsolicited bool     `json:"unsolicited"`
}
