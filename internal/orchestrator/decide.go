// Package orchestrator is the sole authority over workspace structure.
package orchestrator

import (
	"strings"
	"time"

	"deepflow/internal/domain"
	"deepflow/internal/evaluator"
)

// Policy holds the adaptation limits.
type Policy struct {
	AdaptWindow     time.Duration
	ContentCooldown time.Duration
	MinCells        int
	MaxCells        int
}

// DefaultPolicy returns the MVP's limits.
func DefaultPolicy() Policy {
	return Policy{AdaptWindow: 5 * time.Minute, ContentCooldown: 60 * time.Second, MinCells: 6, MaxCells: 9}
}

func isBusy(c domain.Cell, now time.Time) bool { return c.BusyUntil.After(now) }

func toDecision(r evaluator.Recommendation, target string) domain.Decision {
	return domain.Decision{Action: r.Action, CellID: target, CellType: r.CellType, Objective: r.Objective, Reason: r.Reason, Unsolicited: true}
}

// resolve finds the cell a recommendation names, by ID or else by type.
func resolve(ws domain.WorkspaceState, r evaluator.Recommendation) (domain.Cell, bool) {
	for _, c := range ws.Cells {
		if r.CellID != "" && c.ID == r.CellID {
			return c, true
		}
	}
	if r.CellID == "" && r.TargetType != "" {
		for _, c := range ws.Cells {
			if c.Type == r.TargetType {
				return c, true
			}
		}
	}
	return domain.Cell{}, false
}

// Decide applies the Orchestrator's rules to recommendations. It is pure:
// the model proposes, and this function makes the final call. It returns
// decisions to apply now, and those deferred until conditions allow.
func Decide(ws domain.WorkspaceState, recs []evaluator.Recommendation, lastStructural time.Time, lastUpdate map[string]time.Time, now time.Time, p Policy) (apply []domain.Decision, pending []domain.Decision) {
	count := len(ws.Cells)
	windowOpen := now.Sub(lastStructural) >= p.AdaptWindow
	structuralUsed := false
	retired := map[string]bool{}

	defer func() { pending = newestPerTarget(pending) }()

	// Retirements first, so a spawn at the limit can pair with one.
	ordered := make([]evaluator.Recommendation, 0, len(recs))
	for _, r := range recs {
		if r.Action == domain.ActRetire {
			ordered = append(ordered, r)
		}
	}
	for _, r := range recs {
		if r.Action != domain.ActRetire {
			ordered = append(ordered, r)
		}
	}

	for _, r := range ordered {
		if (r.Action == domain.ActSpawn || r.Action == domain.ActReplace) && !domain.KnownCellType(r.CellType) && r.CellType != "" {
			continue
		}
		if r.Action == domain.ActSpawn {
			if r.CellType == "" {
				continue
			}
			if existing, ok := sameTypeSameObjective(ws, r); ok {
				r.Action = domain.ActUpdate
				r.CellID = existing.ID
			} else {
				pairedWithRetire := len(retired) > 0
				if count >= p.MaxCells && !pairedWithRetire {
					continue
				}
				if !pairedWithRetire && (!windowOpen || structuralUsed) {
					pending = append(pending, toDecision(r, ""))
					continue
				}
				apply = append(apply, toDecision(r, ""))
				count++
				structuralUsed = true
				continue
			}
		}

		target, ok := resolve(ws, r)
		if !ok || retired[target.ID] || target.Type == domain.CellDialogue || target.Type == domain.CellJourney {
			continue // these persist for the whole session, and the app updates them itself
		}

		switch r.Action {
		case domain.ActUpdate, domain.ActDeepen, domain.ActSimplify:
			if isBusy(target, now) {
				pending = append(pending, toDecision(r, target.ID))
				continue
			}
			if last, seen := lastUpdate[target.ID]; seen && now.Sub(last) < p.ContentCooldown {
				pending = append(pending, toDecision(r, target.ID))
				continue
			}
			apply = append(apply, toDecision(r, target.ID))

		case domain.ActReplace, domain.ActRetire:
			if r.Action == domain.ActReplace && r.CellType == "" {
				continue
			}
			if target.Pinned {
				apply = append(apply, domain.Decision{Action: domain.ActKeep, CellID: target.ID, Reason: "Pinned cells are kept."})
				continue
			}
			if isBusy(target, now) {
				pending = append(pending, toDecision(r, target.ID))
				continue
			}
			if r.Action == domain.ActRetire && count-1 < p.MinCells {
				apply = append(apply, domain.Decision{Action: domain.ActKeep, CellID: target.ID, Reason: "Keeping the workspace at its minimum size."})
				continue
			}
			if !windowOpen || structuralUsed {
				pending = append(pending, toDecision(r, target.ID))
				continue
			}
			apply = append(apply, toDecision(r, target.ID))
			structuralUsed = true
			if r.Action == domain.ActRetire {
				count--
				retired[target.ID] = true
			}
		}
	}
	return apply, pending
}

func sameTypeSameObjective(ws domain.WorkspaceState, r evaluator.Recommendation) (domain.Cell, bool) {
	var first domain.Cell
	found := false
	for _, c := range ws.Cells {
		if c.Type != r.CellType {
			continue
		}
		if !found {
			first, found = c, true
		}
		if strings.EqualFold(strings.TrimSpace(c.Objective), strings.TrimSpace(r.Objective)) {
			return c, true
		}
	}
	if found && strings.TrimSpace(r.Objective) == "" {
		return first, true
	}
	return domain.Cell{}, false
}

func newestPerTarget(in []domain.Decision) []domain.Decision {
	out := make([]domain.Decision, 0, len(in))
	index := map[string]int{}
	for _, d := range in {
		key := d.CellID
		if key == "" {
			key = "spawn:" + string(d.CellType)
		}
		if i, ok := index[key]; ok {
			out[i] = d
			continue
		}
		index[key] = len(out)
		out = append(out, d)
	}
	return out
}
