package harness

import (
	"testing"

	"deepflow/internal/domain"
)

func TestContextProjectionLimitsWhatCardsSee(t *testing.T) {
	c := NewContextStore(LearningContext{PursuitTitle: "T", Recent: []Interaction{{Kind: "dialogue", Text: "secret"}}})
	if v := c.Project(domain.CellDocument); len(v.Recent) != 0 {
		t.Fatalf("notes saw %v", v.Recent)
	}
	if v := c.Project(domain.CellGame); len(v.Recent) != 1 {
		t.Fatalf("the game should see recent activity: %v", v.Recent)
	}
	if v := c.Project(domain.CellDialogue); len(v.Recent) != 1 {
		t.Fatalf("dialogue saw %v", v.Recent)
	}
	if v := c.Project(domain.CellGame); v.PursuitTitle != "T" {
		t.Fatal("the game needs the topic")
	}
}

func TestContextVersionMonotonic(t *testing.T) {
	c := NewContextStore(LearningContext{})
	a := c.Apply(func(l *LearningContext) { l.Inquiry = "x" })
	b := c.Apply(func(l *LearningContext) { l.Inquiry = "y" })
	if b != a+1 || c.Project(domain.CellDialogue).Version != b {
		t.Fatalf("%d %d", a, b)
	}
}
