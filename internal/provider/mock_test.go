package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMockDeterministic(t *testing.T) {
	m := NewMock()
	run := func() string {
		var b strings.Builder
		_, err := m.Stream(context.Background(), TextRequest{Role: RoleDialogue, Messages: []Turn{{Role: "user", Content: "Indexes are always faster"}}}, func(s string) { b.WriteString(s) })
		if err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	a, b := run(), run()
	if a == "" || a != b {
		t.Fatalf("not deterministic: %q vs %q", a, b)
	}
}

func TestMockStructuredEveryKnownName(t *testing.T) {
	m := NewMock()
	for _, name := range []string{"concept.content", "game.spec", "quiz.batch", "journey.index", "map.places", "thought.scenario", "thought.respond", "evaluator.assess", "session.consolidate", "session.retrieval"} {
		var out map[string]any
		if _, err := m.Structured(context.Background(), StructuredRequest{Name: name, Prompt: "Topic: Databases"}, &out); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if len(out) == 0 {
			t.Errorf("%s: empty output", name)
		}
	}
	var out map[string]any
	if _, err := m.Structured(context.Background(), StructuredRequest{Name: "nope"}, &out); err == nil {
		t.Error("unknown name should fail")
	}
}

func TestMockHonorsFail(t *testing.T) {
	m := NewMock()
	boom := errors.New("boom")
	m.Fail("concept.content", boom)
	var out map[string]any
	if _, err := m.Structured(context.Background(), StructuredRequest{Name: "concept.content"}, &out); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	m.Fail("concept.content", nil)
	if _, err := m.Structured(context.Background(), StructuredRequest{Name: "concept.content"}, &out); err != nil {
		t.Fatalf("cleared fail: %v", err)
	}
}

func TestMockHonorsContextCancel(t *testing.T) {
	m := NewMock()
	m.Delay("concept.content", time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var out map[string]any
	if _, err := m.Structured(ctx, StructuredRequest{Name: "concept.content"}, &out); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestMockStorylineShiftDetected(t *testing.T) {
	m := NewMock()
	var out struct {
		Intent struct {
			Shifted bool `json:"shifted"`
		} `json:"intent"`
		Recommendations []struct {
			Action, Target string
		} `json:"recommendations"`
	}
	_, err := m.Structured(context.Background(), StructuredRequest{Name: "evaluator.assess", Prompt: "context\n<trigger>What if there was no friction?</trigger>"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Intent.Shifted || len(out.Recommendations) == 0 || out.Recommendations[0].Action != "REPLACE" {
		t.Fatalf("%+v", out)
	}
}

func TestMockOpeningPosesAProblem(t *testing.T) {
	m := NewMock()
	var b strings.Builder
	_, err := m.Stream(context.Background(), TextRequest{System: "Topic: Force and Motion", Messages: []Turn{{Role: "user", Content: "Open this session."}}}, func(s string) { b.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "force") {
		t.Fatal(b.String())
	}
}
