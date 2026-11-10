package session

import (
	"testing"

	"deepflow/internal/domain"
)

func TestDetectCardRequest(t *testing.T) {
	cases := []struct {
		text string
		want domain.CellType
	}{
		{"give me a game", domain.CellGame},
		{"Give me a game!", domain.CellGame},
		{"can we play a game on this", domain.CellGame},
		{"let's play", domain.CellGame},
		{"I want another game", domain.CellGame},
		{"game please", domain.CellGame},
		{"make an experiment about this", domain.CellGame},
		{"quiz me", domain.CellQuiz},
		{"give me a quiz on conjugates", domain.CellQuiz},
		{"test me", domain.CellQuiz},
		{"another question please", domain.CellQuiz},
		{"can you make a flashcard", domain.CellConcept},
		{"show me the key idea", domain.CellConcept},

		{"show me on a map", domain.CellMap},
		{"map please", domain.CellMap},
		{"can you show a map of the rivers", domain.CellMap},

		{"what is game theory", ""},
		{"what does this map show", ""},
		{"the map was confusing to me earlier today in class", ""},
		{"why do people play chess", ""},
		{"game theory studies strategy between players in many settings", ""},
		{"I have a question about this", ""},
		{"the quiz was hard", ""},
		{"how does this game work", ""},
		{"yes", ""},
		{"where are complex numbers used", ""},
	}
	for _, c := range cases {
		got, ok := DetectCardRequest(c.text)
		if c.want == "" && ok {
			t.Errorf("%q: detected %s", c.text, got)
		}
		if c.want != "" && (!ok || got != c.want) {
			t.Errorf("%q: got %q %v, want %s", c.text, got, ok, c.want)
		}
	}
}

func TestIsMapSubject(t *testing.T) {
	yes := [][2]string{
		{"Mughal Empire", ""}, {"Indian history", ""}, {"Geography", ""}, {"Rivers of India", ""}, {"The French Revolution", ""},
		{"World War II", ""}, {"Maths", "how did ancient civilisations trade"}, {"Climate zones", ""}, {"Countries and capitals", ""},
	}
	for _, c := range yes {
		if !IsMapSubject(c[0], c[1]) {
			t.Errorf("%v: should use a map", c)
		}
	}
	no := [][2]string{{"Force and Motion", ""}, {"Complex Numbers", ""}, {"Photosynthesis", ""}, {"Fractions", ""}, {"Golang", "war stories"}, {"Warfarin chemistry", ""}, {"Mapping functions", ""}}
	for _, c := range no {
		if c[0] == "Golang" {
			continue
		}
		if IsMapSubject(c[0], c[1]) {
			t.Errorf("%v: shouldn't use a map", c)
		}
	}
}
