package domain

import (
	"encoding/json"
	"time"
)

// EventType names a typed workspace event (spec section 4.1).
type EventType string

const (
	EvUserMessage        EventType = "dialogue.user_message"
	EvAgentMessage       EventType = "dialogue.agent_message"
	EvQuizAnswered       EventType = "quiz.answered"
	EvQuizNext           EventType = "quiz.next"
	EvQuizResult         EventType = "quiz.result"
	EvQuizBatch          EventType = "quiz.batch"
	EvGameLevelWon       EventType = "game.level_won"
	EvGameNext           EventType = "game.next"
	EvGameStar           EventType = "game.star"
	EvJourneySet         EventType = "journey.set"
	EvMapExplored        EventType = "map.explored"
	EvThoughtSubmitted   EventType = "thought.response_submitted"
	EvConceptExplored    EventType = "concept.explored"
	EvObjectEdited       EventType = "document.object_edited"
	EvObjectChallenged   EventType = "document.object_challenged"
	EvRetrievalAnswered  EventType = "retrieval.answered"
	EvCellRequest        EventType = "cell.request"
	EvUserPin            EventType = "user.pin"
	EvUserUnpin          EventType = "user.unpin"
	EvUserRemove         EventType = "user.remove"
	EvUserRefresh        EventType = "user.refresh"
	EvUserUndo           EventType = "user.undo"
	EvUserReassess       EventType = "user.reassess"
	EvOrchestratorDecide EventType = "orchestrator.decision"
	EvContextUpdated     EventType = "context.updated"
)

// LearnerAuthored reports whether an event carries the learner's own words.
func (t EventType) LearnerAuthored() bool {
	switch t {
	case EvUserMessage, EvQuizAnswered, EvThoughtSubmitted,
		EvRetrievalAnswered, EvObjectChallenged, EvObjectEdited:
		return true
	}
	return false
}

// Event is a persisted workspace event.
type Event struct {
	ID             string          `json:"id"`
	SessionID      string          `json:"sessionId"`
	Type           EventType       `json:"type"`
	Origin         string          `json:"origin"`
	DedupeKey      string          `json:"dedupeKey"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	ContextVersion int64           `json:"contextVersion"`
	CreatedAt      time.Time       `json:"createdAt"`
}
