package evaluator

import (
	"encoding/json"
	"strings"
	"testing"

	"deepflow/internal/domain"
)

func src(t domain.EventType, text string) domain.Event {
	b, _ := json.Marshal(map[string]string{"text": text})
	return domain.Event{ID: "e", SessionID: "s2", Type: t, Payload: b}
}

func TestGuardRejectsNonLearnerSource(t *testing.T) {
	for _, ty := range []domain.EventType{domain.EvAgentMessage, domain.EvConceptExplored, domain.EvGameStar, domain.EvQuizResult, domain.EvQuizBatch} {
		if _, ok := Guard(EvidenceClaim{Tier: "moderate"}, src(ty, "long enough text")); ok {
			t.Errorf("%s accepted", ty)
		}
	}
}

func TestGuardDurableRequiresLaterSessionRetrieval(t *testing.T) {
	same := domain.Event{SessionID: "s2", Type: domain.EvRetrievalAnswered, Payload: json.RawMessage(`{"text":"x","fromSession":"s2"}`)}
	c, ok := Guard(EvidenceClaim{Tier: "durable"}, same)
	if !ok || c.Tier != "strong" {
		t.Fatalf("same session: %+v %v", c, ok)
	}
	later := domain.Event{SessionID: "s2", Type: domain.EvRetrievalAnswered, Payload: json.RawMessage(`{"text":"x","fromSession":"s1"}`)}
	c, ok = Guard(EvidenceClaim{Tier: "durable"}, later)
	if !ok || c.Tier != "durable" {
		t.Fatalf("later session: %+v %v", c, ok)
	}
	c, ok = Guard(EvidenceClaim{Tier: "durable"}, src(domain.EvQuizAnswered, "x"))
	if !ok || c.Tier != "strong" {
		t.Fatalf("practice: %+v %v", c, ok)
	}
}

func TestGuardCapsNonSubmissionAtModerate(t *testing.T) {
	c, ok := Guard(EvidenceClaim{Tier: "strong"}, src(domain.EvUserMessage, "short"))
	if !ok || c.Tier != "moderate" {
		t.Fatalf("%+v %v", c, ok)
	}
	c, ok = Guard(EvidenceClaim{Tier: "strong"}, src(domain.EvUserMessage, strings.Repeat("word ", 20)))
	if !ok || c.Tier != "strong" {
		t.Fatalf("substantive: %+v %v", c, ok)
	}
	c, ok = Guard(EvidenceClaim{Tier: "strong"}, src(domain.EvQuizAnswered, "short"))
	if !ok || c.Tier != "strong" {
		t.Fatalf("submission: %+v %v", c, ok)
	}
}

func TestGuardRejectsUnknownTier(t *testing.T) {
	if _, ok := Guard(EvidenceClaim{Tier: "mastered"}, src(domain.EvQuizAnswered, "x")); ok {
		t.Fatal("unknown tier accepted")
	}
}
