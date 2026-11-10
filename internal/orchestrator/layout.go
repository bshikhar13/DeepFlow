package orchestrator

import "deepflow/internal/domain"

const (
	dialogueSpan = 4
	sideColumns  = 12 - dialogueSpan
)

// widthRank orders which cells get the wider slots in a row.
var widthRank = map[domain.CellType]int{
	domain.CellMap: 5, domain.CellGame: 4, domain.CellQuiz: 3, domain.CellThought: 3,
	domain.CellDocument: 2, domain.CellJourney: 2, domain.CellConcept: 1,
}

// rowPlan splits k side cells into rows of two or three.
func rowPlan(k int) []int {
	switch {
	case k <= 0:
		return nil
	case k <= 2:
		return []int{k}
	case k == 3:
		return []int{3}
	case k == 4:
		return []int{2, 2}
	case k == 5:
		return []int{2, 3}
	case k == 6:
		return []int{3, 3}
	case k == 7:
		return []int{2, 2, 3}
	}
	return []int{3, 3, k - 6}
}

// spansFor shares the eight side columns across a row, widest to the top rank.
func spansFor(cells []domain.Cell) []int {
	switch len(cells) {
	case 1:
		return []int{sideColumns}
	case 2:
		if widthRank[cells[1].Type] > widthRank[cells[0].Type] {
			return []int{3, 5}
		}
		return []int{5, 3}
	}
	spans := []int{3, 3, 3}
	narrow := 0
	for i, c := range cells {
		if widthRank[c.Type] < widthRank[cells[narrow].Type] {
			narrow = i
		}
	}
	spans[narrow] = 2
	return spans
}

// Reflow assigns grid positions in cell order: Chat takes the left four columns
// at full height, and other cells fill the remaining eight, with no gaps.
func Reflow(cells []domain.Cell) []domain.Cell {
	out := make([]domain.Cell, len(cells))
	copy(out, cells)
	var others []int
	dialogue := -1
	for i, c := range out {
		if c.Type == domain.CellDialogue && dialogue < 0 {
			dialogue = i
		} else {
			others = append(others, i)
		}
	}
	plan := rowPlan(len(others))
	rows := len(plan)
	if rows < 1 {
		rows = 1
	}
	if dialogue >= 0 {
		out[dialogue].Layout = domain.Layout{Col: 1, Row: 1, ColSpan: dialogueSpan, RowSpan: rows}
	}
	next := 0
	for r, n := range plan {
		row := make([]domain.Cell, n)
		for i := 0; i < n; i++ {
			row[i] = out[others[next+i]]
		}
		col := dialogueSpan + 1
		for i, span := range spansFor(row) {
			out[others[next+i]].Layout = domain.Layout{Col: col, Row: r + 1, ColSpan: span, RowSpan: 1}
			col += span
		}
		next += n
	}
	return out
}

func newCell(t domain.CellType, objective string) domain.Cell {
	return domain.Cell{
		ID: domain.NewID("cell"), Type: t, Objective: objective,
		AgentInstanceID: domain.NewID("agent"), Status: domain.StatusProposed,
	}
}

// InitialCells is the default composition for a new session.
func InitialCells(inquiry string) []domain.Cell { return InitialCellsFor(inquiry, false) }

// InitialCellsFor composes a new session. Where places matter, a map takes the
// wide slot and the Key Idea makes way, keeping the workspace uncrowded.
func InitialCellsFor(inquiry string, maps bool) []domain.Cell {
	about := inquiry
	if about == "" {
		about = "the main ideas of this subject"
	}
	if maps {
		return Reflow([]domain.Cell{
			newCell(domain.CellDialogue, "Chat about: "+about),
			newCell(domain.CellMap, "Show the places behind: "+about),
			newCell(domain.CellJourney, "Show the path and the next milestone"),
			newCell(domain.CellGame, "Play with: "+about),
			newCell(domain.CellQuiz, "Quiz on: "+about),
			newCell(domain.CellDocument, "Keep the student's ideas and questions"),
		})
	}
	return Reflow([]domain.Cell{
		newCell(domain.CellDialogue, "Chat about: "+about),
		newCell(domain.CellGame, "Play with: "+about),
		newCell(domain.CellJourney, "Show the path and the next milestone"),
		newCell(domain.CellQuiz, "Quiz on: "+about),
		newCell(domain.CellConcept, "Key idea behind: "+about),
		newCell(domain.CellDocument, "Keep the student's ideas and questions"),
	})
}
