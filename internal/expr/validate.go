package expr

import (
	"errors"
	"fmt"
	"math"

	"deepflow/internal/domain"
)

const gridPerParam = 21

// ValidateGame checks a game spec is safe and winnable before a learner sees it.
func ValidateGame(g domain.GameSpec) error {
	switch g.Kind {
	case "target":
		return validateTarget(g)
	case "sort":
		return validateSort(g)
	case "locate":
		return validateLocate(g)
	}
	return fmt.Errorf("unknown game kind %q", g.Kind)
}

func validateTarget(g domain.GameSpec) error {
	if g.Basis != "illustrative" {
		return fmt.Errorf("basis must be illustrative, got %q", g.Basis)
	}
	if len(g.Params) == 0 || len(g.Params) > 3 {
		return errors.New("a target game needs 1 to 3 sliders")
	}
	if len(g.Levels) == 0 || len(g.Levels) > 5 {
		return errors.New("a target game needs 1 to 5 levels")
	}
	names := make([]string, 0, len(g.Params))
	seen := map[string]bool{}
	start := map[string]float64{}
	for _, p := range g.Params {
		if p.ID == "" || IsFunction(p.ID) || seen[p.ID] {
			return fmt.Errorf("invalid slider id %q", p.ID)
		}
		if !(p.Min < p.Max) || !(p.Step > 0) || p.Value < p.Min || p.Value > p.Max {
			return fmt.Errorf("slider %q has an invalid range", p.ID)
		}
		seen[p.ID] = true
		names = append(names, p.ID)
		start[p.ID] = p.Value
	}
	e, err := Parse(g.Output.Expr, names)
	if err != nil {
		return fmt.Errorf("output: %w", err)
	}
	for i, l := range g.Levels {
		if !(l.Tolerance > 0) {
			return fmt.Errorf("level %d needs a positive tolerance", i+1)
		}
		if !reachable(e, g.Params, l) {
			return fmt.Errorf("level %d can't be won with these sliders", i+1)
		}
	}
	if math.Abs(e.Eval(start)-g.Levels[0].Target) <= g.Levels[0].Tolerance {
		return errors.New("level 1 is already won at the starting values")
	}
	return nil
}

// reachable samples the slider ranges, on values a slider can actually land on.
func reachable(e *Expr, params []domain.GameParam, l domain.GameLevel) bool {
	vars := map[string]float64{}
	var walk func(i int) bool
	walk = func(i int) bool {
		if i == len(params) {
			v := e.Eval(vars)
			return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v-l.Target) <= l.Tolerance
		}
		p := params[i]
		steps := int(math.Round((p.Max - p.Min) / p.Step))
		stride := 1
		if steps >= gridPerParam {
			stride = steps / (gridPerParam - 1)
		}
		for s := 0; s <= steps; s += stride {
			vars[p.ID] = p.Min + float64(s)*p.Step
			if walk(i + 1) {
				return true
			}
		}
		return false
	}
	return walk(0)
}

func validateSort(g domain.GameSpec) error {
	labels := map[string]bool{}
	for _, it := range g.Items {
		if it.Label == "" || labels[it.Label] {
			return fmt.Errorf("invalid or repeated item %q", it.Label)
		}
		labels[it.Label] = true
	}
	switch g.Mode {
	case "order":
		if len(g.Items) < 3 || len(g.Items) > 6 {
			return errors.New("an ordering game needs 3 to 6 items")
		}
	case "groups":
		if len(g.Groups) < 2 || len(g.Groups) > 3 {
			return errors.New("a grouping game needs 2 or 3 groups")
		}
		if len(g.Items) < 4 || len(g.Items) > 8 {
			return errors.New("a grouping game needs 4 to 8 items")
		}
		used := map[string]int{}
		for _, it := range g.Items {
			used[it.Group]++
		}
		for _, name := range g.Groups {
			if used[name] == 0 {
				return fmt.Errorf("group %q has no items", name)
			}
			delete(used, name)
		}
		if len(used) > 0 {
			return errors.New("an item is in a group that doesn't exist")
		}
	default:
		return fmt.Errorf("unknown sort mode %q", g.Mode)
	}
	return nil
}

// Attempt is what a client claims won a level. VerifyWin recomputes the result,
// so a star means the game was really solved.
type Attempt struct {
	Level  int                `json:"level"`
	Values map[string]float64 `json:"values,omitempty"`
	Order  []string           `json:"order,omitempty"`
	Placed map[string]string  `json:"placed,omitempty"`
	Point  *GeoPoint          `json:"point,omitempty"`
}

// VerifyWin reports whether an attempt really solves the given level.
func VerifyWin(g domain.GameSpec, a Attempt) bool {
	switch g.Kind {
	case "target":
		return verifyTarget(g, a)
	case "locate":
		return verifyLocate(g, a)
	}
	if a.Level != 0 {
		return false
	}
	switch g.Mode {
	case "order":
		if len(a.Order) != len(g.Items) {
			return false
		}
		for i, it := range g.Items {
			if a.Order[i] != it.Label {
				return false
			}
		}
		return true
	case "groups":
		for _, it := range g.Items {
			if a.Placed[it.Label] != it.Group {
				return false
			}
		}
		return len(g.Items) > 0
	}
	return false
}

func verifyTarget(g domain.GameSpec, a Attempt) bool {
	if a.Level < 0 || a.Level >= len(g.Levels) {
		return false
	}
	names := make([]string, 0, len(g.Params))
	for _, p := range g.Params {
		v, ok := a.Values[p.ID]
		if !ok || v < p.Min || v > p.Max {
			return false
		}
		names = append(names, p.ID)
	}
	e, err := Parse(g.Output.Expr, names)
	if err != nil {
		return false
	}
	v := e.Eval(a.Values)
	l := g.Levels[a.Level]
	return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v-l.Target) <= l.Tolerance
}

// GeoPoint is a position on Earth.
type GeoPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

const earthRadiusKm = 6371

// DistanceKm is the great-circle distance between two points. web/src/lib/geo.ts
// computes the same thing for instant feedback; this one decides wins.
func DistanceKm(a, b GeoPoint) float64 {
	rad := math.Pi / 180
	dLat, dLon := (b.Lat-a.Lat)*rad, (b.Lon-a.Lon)*rad
	h := math.Pow(math.Sin(dLat/2), 2) + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Pow(math.Sin(dLon/2), 2)
	return 2 * earthRadiusKm * math.Asin(math.Min(1, math.Sqrt(h)))
}

// ValidatePlaces checks a list of map places.
func ValidatePlaces(places []domain.GamePlace, min, max int) error {
	if len(places) < min || len(places) > max {
		return fmt.Errorf("need %d to %d places, got %d", min, max, len(places))
	}
	seen := map[string]bool{}
	for _, p := range places {
		if p.Label == "" || seen[p.Label] {
			return fmt.Errorf("invalid or repeated place %q", p.Label)
		}
		seen[p.Label] = true
		if p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 || math.IsNaN(p.Lat) || math.IsNaN(p.Lon) {
			return fmt.Errorf("place %q has impossible coordinates", p.Label)
		}
	}
	return nil
}

func validateLocate(g domain.GameSpec) error {
	if err := ValidatePlaces(g.Places, 2, 6); err != nil {
		return err
	}
	if g.ToleranceKm < 30 || g.ToleranceKm > 2000 {
		return fmt.Errorf("tolerance %v km is out of range", g.ToleranceKm)
	}
	return nil
}

func verifyLocate(g domain.GameSpec, a Attempt) bool {
	if a.Point == nil || a.Level < 0 || a.Level >= len(g.Places) {
		return false
	}
	p := g.Places[a.Level]
	return DistanceKm(*a.Point, GeoPoint{Lat: p.Lat, Lon: p.Lon}) <= g.ToleranceKm
}
