package harness

import (
	"context"
	"encoding/json"

	"deepflow/internal/domain"
	"deepflow/internal/provider"
)

// AgentInput is everything an agent receives for one task.
type AgentInput struct {
	Cell     domain.Cell
	Context  ContextView
	Provider provider.Provider
	History  []provider.Turn // dialogue history; only set for Dialogue
	Delta    func(string)    // streams text into this cell
}

// AgentOutput is an agent's result. Nil content or state leaves it unchanged.
// There is deliberately no way to address another cell.
type AgentOutput struct {
	Content    json.RawMessage
	LocalState json.RawMessage
	Publish    []domain.Event
	Requests   []CellRequest
	Then       *Directive // follow-up work for this same agent, queued after this result
}

// CellRequest asks the Orchestrator for something beyond the agent's authority.
type CellRequest struct {
	Kind      string          `json:"kind"` // spawn, replace, retire, space, challenge
	Reason    string          `json:"reason"`
	CellType  domain.CellType `json:"cellType,omitempty"`
	Objective string          `json:"objective,omitempty"`
}

// Directive is an Orchestrator instruction to adapt content.
type Directive struct {
	Kind      string `json:"kind"` // update, deepen, simplify, refresh
	Objective string `json:"objective"`
}

// Agent governs one cell's learning experience.
type Agent interface {
	Type() domain.CellType
	Init(ctx context.Context, in AgentInput) (AgentOutput, error)
	Handle(ctx context.Context, in AgentInput, ev domain.Event) (AgentOutput, error)
	Update(ctx context.Context, in AgentInput, d Directive) (AgentOutput, error)
	Retain(content, state json.RawMessage) []domain.Artifact
}

// AgentFactory creates a fresh agent for a cell type.
type AgentFactory func(domain.CellType) Agent

// Sink receives cell changes from the runtime.
type Sink interface {
	CellUpdated(domain.Cell)
	CellStatus(cellID string, s domain.CellStatus)
	Delta(cellID, text string)
	Request(cellID string, r CellRequest)
}
