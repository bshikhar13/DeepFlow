package orchestrator

import (
	"fmt"
	"testing"

	"deepflow/internal/domain"
)

func TestReflowNoOverlapAndDialogueSpans4(t *testing.T) {
	types := []domain.CellType{domain.CellGame, domain.CellConcept, domain.CellQuiz, domain.CellThought, domain.CellJourney, domain.CellDocument, domain.CellConcept, domain.CellQuiz}
	for n := 6; n <= 9; n++ {
		cells := []domain.Cell{{ID: "d", Type: domain.CellDialogue}}
		for i := 0; i < n-1; i++ {
			cells = append(cells, domain.Cell{ID: fmt.Sprintf("c%d", i), Type: types[i]})
		}
		out := Reflow(cells)
		occupied := map[[2]int]string{}
		maxRow := 0
		for _, c := range out {
			l := c.Layout
			if l.Col < 1 || l.ColSpan < 1 || l.Col+l.ColSpan-1 > 12 || l.Row < 1 || l.RowSpan < 1 {
				t.Fatalf("n=%d bad layout %+v", n, l)
			}
			for x := l.Col; x < l.Col+l.ColSpan; x++ {
				for y := l.Row; y < l.Row+l.RowSpan; y++ {
					k := [2]int{x, y}
					if occupied[k] != "" {
						t.Fatalf("n=%d overlap %v: %s and %s", n, k, occupied[k], c.ID)
					}
					occupied[k] = c.ID
				}
			}
			if end := l.Row + l.RowSpan - 1; end > maxRow {
				maxRow = end
			}
		}
		if d := out[0].Layout; d.ColSpan != 4 || d.RowSpan != maxRow {
			t.Fatalf("n=%d dialogue %+v rows %d", n, d, maxRow)
		}
		if len(occupied) != 12*maxRow {
			t.Fatalf("n=%d leaves gaps: %d of %d", n, len(occupied), 12*maxRow)
		}
	}
}

func TestDefaultLayoutGivesGameTheWidestSlot(t *testing.T) {
	cells := InitialCells("Why?")
	spans := map[domain.CellType]int{}
	for _, c := range cells {
		spans[c.Type] = c.Layout.ColSpan
	}
	if spans[domain.CellGame] != 5 || spans[domain.CellJourney] != 3 || spans[domain.CellConcept] != 2 || spans[domain.CellQuiz] != 3 || spans[domain.CellDocument] != 3 {
		t.Fatalf("%v", spans)
	}
}

func TestInitialCellsSixWithDialogueUsable(t *testing.T) {
	cells := InitialCells("Why?")
	if len(cells) != 6 || cells[0].Type != domain.CellDialogue {
		t.Fatalf("%+v", cells)
	}
	seen := map[domain.CellType]bool{}
	for _, c := range cells {
		if c.ID == "" || c.AgentInstanceID == "" || c.Objective == "" {
			t.Fatalf("%+v", c)
		}
		seen[c.Type] = true
	}
	if len(seen) != 6 {
		t.Fatalf("%v", seen)
	}
}

func TestMapSubjectsGetAMapInTheWideSlot(t *testing.T) {
	cells := InitialCellsFor("Why?", true)
	spans := map[domain.CellType]int{}
	for _, c := range cells {
		spans[c.Type] = c.Layout.ColSpan
	}
	if len(cells) != 6 || spans[domain.CellMap] != 5 || spans[domain.CellJourney] != 3 || spans[domain.CellConcept] != 0 {
		t.Fatalf("%v", spans)
	}
	if spans[domain.CellGame] != 3 || spans[domain.CellQuiz] != 3 || spans[domain.CellDocument] != 2 {
		t.Fatalf("%v", spans)
	}
}
