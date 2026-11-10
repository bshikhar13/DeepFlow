package domain

import "testing"

func TestJourneyAddStep(t *testing.T) {
	j := &Journey{Topics: []JourneyTopic{{Title: "A", Goal: 2}, {Title: "B", Goal: 1}}}
	if j.AddStep() != nil || j.Topics[0].Progress != 1 || j.Current != 0 {
		t.Fatalf("%+v", j)
	}
	done := j.AddStep()
	if done == nil || done.Title != "A" || !j.Topics[0].Done || j.Current != 1 {
		t.Fatalf("%+v %+v", done, j)
	}
	if cur, ok := j.CurrentTopic(); !ok || cur.Title != "B" {
		t.Fatalf("%+v", cur)
	}
	if j.AddStep() == nil || !j.Complete() {
		t.Fatalf("%+v", j)
	}
	if j.AddStep() != nil {
		t.Fatal("a finished journey earns no more steps")
	}
	if _, ok := j.CurrentTopic(); ok {
		t.Fatal("no current topic when complete")
	}
}

func TestNilJourneyIsSafe(t *testing.T) {
	var j *Journey
	if j.AddStep() != nil {
		t.Fatal("nil journey")
	}
	if _, ok := j.CurrentTopic(); ok {
		t.Fatal("nil journey")
	}
}
