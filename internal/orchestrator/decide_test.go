package orchestrator

import (
	"testing"
	"time"

	"deepflow/internal/domain"
	"deepflow/internal/evaluator"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func sevenCells() domain.WorkspaceState {
	return domain.WorkspaceState{Cells: []domain.Cell{
		{ID: "dlg", Type: domain.CellDialogue},
		{ID: "viz", Type: domain.CellGame, Objective: "viz obj"},
		{ID: "con", Type: domain.CellConcept, Objective: "con obj"},
		{ID: "pra", Type: domain.CellQuiz, Objective: "pra obj"},
		{ID: "tho", Type: domain.CellThought, Objective: "tho obj"},
		{ID: "und", Type: domain.CellJourney},
		{ID: "doc", Type: domain.CellDocument},
	}}
}

type rec = evaluator.Recommendation

func TestDecide(t *testing.T) {
	longAgo := t0.Add(-time.Hour)
	p := DefaultPolicy()
	cases := []struct {
		name           string
		mod            func(*domain.WorkspaceState)
		recs           []rec
		lastStructural time.Time
		lastUpdate     map[string]time.Time
		wantApply      []domain.Action
		wantPending    int
	}{
		{
			name: "dialogue_never_retired",
			recs: []rec{{Action: domain.ActRetire, CellID: "dlg"}, {Action: domain.ActReplace, TargetType: domain.CellDialogue, CellType: domain.CellConcept}},
		},
		{
			name:      "pinned_not_replaced",
			mod:       func(w *domain.WorkspaceState) { w.Cells[2].Pinned = true },
			recs:      []rec{{Action: domain.ActReplace, CellID: "con", CellType: domain.CellGame}},
			wantApply: []domain.Action{domain.ActKeep},
		},
		{
			name:        "busy_deferred_to_pending",
			mod:         func(w *domain.WorkspaceState) { w.Cells[2].BusyUntil = t0.Add(time.Minute) },
			recs:        []rec{{Action: domain.ActReplace, CellID: "con", CellType: domain.CellGame}},
			wantPending: 1,
		},
		{
			name:        "busy_content_update_deferred",
			mod:         func(w *domain.WorkspaceState) { w.Cells[3].BusyUntil = t0.Add(time.Minute) },
			recs:        []rec{{Action: domain.ActUpdate, CellID: "pra"}},
			wantPending: 1,
		},
		{
			name:        "one_structural_per_window",
			recs:        []rec{{Action: domain.ActReplace, CellID: "con", CellType: domain.CellGame}, {Action: domain.ActReplace, CellID: "tho", CellType: domain.CellConcept}},
			wantApply:   []domain.Action{domain.ActReplace},
			wantPending: 1,
		},
		{
			name:           "structural_inside_window_pending",
			lastStructural: t0.Add(-time.Minute),
			recs:           []rec{{Action: domain.ActReplace, CellID: "con", CellType: domain.CellGame}},
			wantPending:    1,
		},
		{
			name:           "pending_keeps_newest_per_cell",
			lastStructural: t0.Add(-time.Minute),
			recs:           []rec{{Action: domain.ActReplace, CellID: "con", CellType: domain.CellGame, Objective: "old"}, {Action: domain.ActReplace, CellID: "con", CellType: domain.CellQuiz, Objective: "new"}},
			wantPending:    1,
		},
		{
			name:        "content_update_rate_limited",
			lastUpdate:  map[string]time.Time{"pra": t0.Add(-30 * time.Second)},
			recs:        []rec{{Action: domain.ActUpdate, CellID: "pra"}},
			wantPending: 1,
		},
		{
			name:       "content_update_allowed_after_cooldown",
			lastUpdate: map[string]time.Time{"pra": t0.Add(-61 * time.Second)},
			recs:       []rec{{Action: domain.ActDeepen, TargetType: domain.CellQuiz}},
			wantApply:  []domain.Action{domain.ActDeepen},
		},
		{
			name:           "content_update_not_counted_structural",
			lastStructural: t0.Add(-time.Minute),
			recs:           []rec{{Action: domain.ActUpdate, CellID: "pra"}, {Action: domain.ActSimplify, CellID: "con"}},
			wantApply:      []domain.Action{domain.ActUpdate, domain.ActSimplify},
		},
		{
			name:      "spawn_existing_type_becomes_update",
			recs:      []rec{{Action: domain.ActSpawn, CellType: domain.CellQuiz, Objective: "pra obj"}},
			wantApply: []domain.Action{domain.ActUpdate},
		},
		{
			name:      "spawn_same_type_distinct_objective_allowed",
			recs:      []rec{{Action: domain.ActSpawn, CellType: domain.CellQuiz, Objective: "a different objective"}},
			wantApply: []domain.Action{domain.ActSpawn},
		},
		{
			name: "retire_below_min_becomes_keep",
			mod: func(w *domain.WorkspaceState) {
				w.Cells = w.Cells[:6]
			},
			recs:      []rec{{Action: domain.ActRetire, CellID: "con"}},
			wantApply: []domain.Action{domain.ActKeep},
		},
		{
			name:      "retire_above_min_allowed",
			recs:      []rec{{Action: domain.ActRetire, CellID: "con"}},
			wantApply: []domain.Action{domain.ActRetire},
		},
		{
			name: "spawn_at_max_rejected",
			mod: func(w *domain.WorkspaceState) {
				w.Cells = append(w.Cells, domain.Cell{ID: "x", Type: domain.CellConcept}, domain.Cell{ID: "y", Type: domain.CellThought})
			},
			recs: []rec{{Action: domain.ActSpawn, CellType: domain.CellGame, Objective: "new one"}},
		},
		{
			name: "spawn_at_max_with_retire_allowed",
			mod: func(w *domain.WorkspaceState) {
				w.Cells = append(w.Cells, domain.Cell{ID: "x", Type: domain.CellConcept}, domain.Cell{ID: "y", Type: domain.CellThought})
			},
			recs:      []rec{{Action: domain.ActSpawn, CellType: domain.CellGame, Objective: "new one"}, {Action: domain.ActRetire, CellID: "con"}},
			wantApply: []domain.Action{domain.ActRetire, domain.ActSpawn},
		},
		{
			name: "journey_never_changed",
			recs: []rec{{Action: domain.ActRetire, CellID: "und"}, {Action: domain.ActReplace, CellID: "und", CellType: domain.CellConcept}},
		},
		{
			name: "unknown_cell_type_ignored",
			recs: []rec{{Action: domain.ActSpawn, CellType: "hologram", Objective: "x"}, {Action: domain.ActReplace, CellID: "con", CellType: "hologram"}},
		},
		{
			name: "unknown_target_ignored",
			recs: []rec{{Action: domain.ActUpdate, CellID: "ghost"}},
		},
		{
			name: "replace_without_new_type_ignored",
			recs: []rec{{Action: domain.ActReplace, CellID: "con"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ws := sevenCells()
			if c.mod != nil {
				c.mod(&ws)
			}
			last := c.lastStructural
			if last.IsZero() {
				last = longAgo
			}
			apply, pending := Decide(ws, c.recs, last, c.lastUpdate, t0, p)
			var got []domain.Action
			for _, d := range apply {
				got = append(got, d.Action)
			}
			if len(got) != len(c.wantApply) {
				t.Fatalf("apply %v want %v", got, c.wantApply)
			}
			for i := range got {
				if got[i] != c.wantApply[i] {
					t.Fatalf("apply %v want %v", got, c.wantApply)
				}
			}
			if len(pending) != c.wantPending {
				t.Fatalf("pending %+v want %d", pending, c.wantPending)
			}
		})
	}
}

func TestDecidePendingNewestWins(t *testing.T) {
	_, pending := Decide(sevenCells(), []rec{
		{Action: domain.ActReplace, CellID: "con", CellType: domain.CellGame, Objective: "old"},
		{Action: domain.ActReplace, CellID: "con", CellType: domain.CellQuiz, Objective: "new"},
	}, t0, nil, t0, DefaultPolicy())
	if len(pending) != 1 || pending[0].Objective != "new" {
		t.Fatalf("%+v", pending)
	}
}

func TestDecideResolvesTargetType(t *testing.T) {
	apply, _ := Decide(sevenCells(), []rec{{Action: domain.ActReplace, TargetType: domain.CellConcept, CellType: domain.CellGame, Reason: "r"}}, t0.Add(-time.Hour), nil, t0, DefaultPolicy())
	if len(apply) != 1 || apply[0].CellID != "con" || apply[0].CellType != domain.CellGame || apply[0].Reason != "r" || !apply[0].Unsolicited {
		t.Fatalf("%+v", apply)
	}
}

func TestDecideIsPure(t *testing.T) {
	ws := sevenCells()
	recs := []rec{{Action: domain.ActRetire, CellID: "con"}}
	Decide(ws, recs, t0.Add(-time.Hour), nil, t0, DefaultPolicy())
	if len(ws.Cells) != 7 {
		t.Fatal("Decide mutated the workspace")
	}
}
