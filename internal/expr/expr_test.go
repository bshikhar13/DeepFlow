package expr

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"deepflow/internal/domain"
)

type casesFile struct {
	Valid []struct {
		Src  string             `json:"src"`
		Vars map[string]float64 `json:"vars"`
		Want *float64           `json:"want"`
	} `json:"valid"`
	Invalid []struct {
		Src     string   `json:"src"`
		Allowed []string `json:"allowed"`
	} `json:"invalid"`
}

func TestConformanceCases(t *testing.T) {
	b, err := os.ReadFile("testdata/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var c casesFile
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	for _, v := range c.Valid {
		names := make([]string, 0, len(v.Vars))
		for k := range v.Vars {
			names = append(names, k)
		}
		e, err := Parse(v.Src, names)
		if err != nil {
			t.Errorf("%q: %v", v.Src, err)
			continue
		}
		got := e.Eval(v.Vars)
		if v.Want == nil {
			if !math.IsNaN(got) && !math.IsInf(got, 0) {
				t.Errorf("%q = %v, want non-finite", v.Src, got)
			}
		} else if math.Abs(got-*v.Want) > 1e-9 {
			t.Errorf("%q = %v, want %v", v.Src, got, *v.Want)
		}
	}
	for _, v := range c.Invalid {
		if _, err := Parse(v.Src, v.Allowed); err == nil {
			t.Errorf("%q: expected parse error", truncate(v.Src))
		}
	}
}

func truncate(s string) string {
	if len(s) > 30 {
		return s[:30] + "…"
	}
	return s
}

func TestLengthCapIs200(t *testing.T) {
	ok := strings.Repeat("1+", 99) + "12" // exactly 200 characters
	if len(ok) != 200 {
		t.Fatalf("setup: %d", len(ok))
	}
	if _, err := Parse(ok, nil); err != nil {
		t.Fatalf("200 chars should parse: %v", err)
	}
	if _, err := Parse(ok+"1", nil); err == nil {
		t.Fatal("201 chars should fail")
	}
}

func TestDeepNestingRejected(t *testing.T) {
	src := strings.Repeat("(", 90) + "1" + strings.Repeat(")", 90)
	if _, err := Parse(src, nil); err == nil {
		t.Fatal("excessive nesting should fail")
	}
}

func target() domain.GameSpec {
	return domain.GameSpec{
		Kind: "target", Title: "t", Goal: "g", Basis: "illustrative",
		Params: []domain.GameParam{{ID: "f", Label: "Force", Min: 0, Max: 100, Step: 1, Value: 10}, {ID: "m", Label: "Mass", Min: 1, Max: 20, Step: 1, Value: 10}},
		Output: domain.GameOutput{Label: "Acceleration", Expr: "f/m", Unit: "m/s²"},
		Levels: []domain.GameLevel{{Target: 5, Tolerance: 0.2}, {Target: 2, Tolerance: 0.1}},
	}
}

func sortOrder() domain.GameSpec {
	return domain.GameSpec{Kind: "sort", Title: "t", Goal: "g", Mode: "order", Items: []domain.GameItem{{Label: "a"}, {Label: "b"}, {Label: "c"}}}
}

func sortGroups() domain.GameSpec {
	return domain.GameSpec{Kind: "sort", Title: "t", Goal: "g", Mode: "groups", Groups: []string{"x", "y"},
		Items: []domain.GameItem{{Label: "a", Group: "x"}, {Label: "b", Group: "y"}, {Label: "c", Group: "x"}, {Label: "d", Group: "y"}}}
}

func TestValidateGameAcceptsAllKinds(t *testing.T) {
	for _, g := range []domain.GameSpec{target(), sortOrder(), sortGroups()} {
		if err := ValidateGame(g); err != nil {
			t.Errorf("%s/%s: %v", g.Kind, g.Mode, err)
		}
	}
}

func TestValidateGameRejects(t *testing.T) {
	cases := map[string]func() domain.GameSpec{
		"unknown kind":         func() domain.GameSpec { g := target(); g.Kind = "shooter"; return g },
		"unknown identifier":   func() domain.GameSpec { g := target(); g.Output.Expr = "f*z"; return g },
		"never finite":         func() domain.GameSpec { g := target(); g.Output.Expr = "1/0"; return g },
		"no levels":            func() domain.GameSpec { g := target(); g.Levels = nil; return g },
		"no params":            func() domain.GameSpec { g := target(); g.Params = nil; return g },
		"unreachable level":    func() domain.GameSpec { g := target(); g.Levels[1].Target = 5000; return g },
		"zero tolerance":       func() domain.GameSpec { g := target(); g.Levels[0].Tolerance = 0; return g },
		"already won at start": func() domain.GameSpec { g := target(); g.Levels[0].Target = 1; return g },
		"bad slider":           func() domain.GameSpec { g := target(); g.Params[0].Step = 0; return g },
		"value out of range":   func() domain.GameSpec { g := target(); g.Params[0].Value = 500; return g },
		"param shadows func":   func() domain.GameSpec { g := target(); g.Params[0].ID = "min"; g.Output.Expr = "m"; return g },
		"not illustrative":     func() domain.GameSpec { g := target(); g.Basis = "validated"; return g },
		"too few to order":     func() domain.GameSpec { g := sortOrder(); g.Items = g.Items[:2]; return g },
		"duplicate labels":     func() domain.GameSpec { g := sortOrder(); g.Items[1].Label = "a"; return g },
		"bad mode":             func() domain.GameSpec { g := sortOrder(); g.Mode = "maze"; return g },
		"one group":            func() domain.GameSpec { g := sortGroups(); g.Groups = []string{"x"}; return g },
		"item in no group":     func() domain.GameSpec { g := sortGroups(); g.Items[0].Group = "z"; return g },
		"empty group":          func() domain.GameSpec { g := sortGroups(); g.Groups = []string{"x", "y", "z"}; return g },
	}
	for name, mk := range cases {
		if err := ValidateGame(mk()); err == nil {
			t.Errorf("%s: should be rejected", name)
		}
	}
}

func TestValidateGameAllowsPartlyUndefinedModel(t *testing.T) {
	g := target()
	g.Params[0].Min = -5
	g.Output.Expr = "sqrt(f)/m"
	g.Levels = []domain.GameLevel{{Target: 3, Tolerance: 0.2}}
	if err := ValidateGame(g); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyWin(t *testing.T) {
	g := target()
	vals := func(f, m float64) Attempt { return Attempt{Level: 0, Values: map[string]float64{"f": f, "m": m}} }
	if !VerifyWin(g, vals(50, 10)) {
		t.Error("on target should win")
	}
	if VerifyWin(g, vals(10, 10)) {
		t.Error("off target should not win")
	}
	if VerifyWin(g, vals(500, 100)) {
		t.Error("values outside the sliders should not win")
	}
	if VerifyWin(g, Attempt{Level: 0, Values: map[string]float64{"f": 50}}) {
		t.Error("a missing slider should not win")
	}
	if VerifyWin(g, Attempt{Level: 9, Values: map[string]float64{"f": 50, "m": 10}}) {
		t.Error("a missing level should not win")
	}
	if !VerifyWin(sortOrder(), Attempt{Order: []string{"a", "b", "c"}}) {
		t.Error("right order should win")
	}
	if VerifyWin(sortOrder(), Attempt{Order: []string{"b", "a", "c"}}) || VerifyWin(sortOrder(), Attempt{Order: []string{"a", "b"}}) {
		t.Error("wrong or short order should not win")
	}
	if !VerifyWin(sortGroups(), Attempt{Placed: map[string]string{"a": "x", "b": "y", "c": "x", "d": "y"}}) {
		t.Error("right groups should win")
	}
	if VerifyWin(sortGroups(), Attempt{Placed: map[string]string{"a": "y", "b": "y", "c": "x", "d": "y"}}) {
		t.Error("wrong groups should not win")
	}
	if VerifyWin(sortOrder(), Attempt{Level: 1, Order: []string{"a", "b", "c"}}) {
		t.Error("a sort game has one level")
	}
}

func locate() domain.GameSpec {
	return domain.GameSpec{Kind: "locate", Title: "t", Goal: "g", ToleranceKm: 300, Places: []domain.GamePlace{
		{Label: "Delhi", Lat: 28.6, Lon: 77.2}, {Label: "Chennai", Lat: 13.1, Lon: 80.3},
	}}
}

func TestValidateLocateGame(t *testing.T) {
	if err := ValidateGame(locate()); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*domain.GameSpec){
		"one place":     func(g *domain.GameSpec) { g.Places = g.Places[:1] },
		"bad latitude":  func(g *domain.GameSpec) { g.Places[0].Lat = 95 },
		"bad longitude": func(g *domain.GameSpec) { g.Places[0].Lon = -200 },
		"duplicate":     func(g *domain.GameSpec) { g.Places[1].Label = "Delhi" },
		"no label":      func(g *domain.GameSpec) { g.Places[1].Label = "" },
		"tolerance low": func(g *domain.GameSpec) { g.ToleranceKm = 1 },
		"tolerance big": func(g *domain.GameSpec) { g.ToleranceKm = 9000 },
	}
	for name, mod := range bad {
		g := locate()
		mod(&g)
		if ValidateGame(g) == nil {
			t.Errorf("%s: should be rejected", name)
		}
	}
}

func TestVerifyWinLocate(t *testing.T) {
	g := locate()
	near := Attempt{Level: 0, Point: &GeoPoint{Lat: 28, Lon: 77}}
	if !VerifyWin(g, near) {
		t.Error("close to Delhi should win")
	}
	if VerifyWin(g, Attempt{Level: 1, Point: &GeoPoint{Lat: 28, Lon: 77}}) {
		t.Error("Delhi isn't Chennai")
	}
	if VerifyWin(g, Attempt{Level: 0}) {
		t.Error("no point should not win")
	}
	if VerifyWin(g, Attempt{Level: 5, Point: &GeoPoint{Lat: 28, Lon: 77}}) {
		t.Error("no such level")
	}
}

func TestDistanceKm(t *testing.T) {
	// Delhi to Mumbai is about 1150 km.
	d := DistanceKm(GeoPoint{Lat: 28.6, Lon: 77.2}, GeoPoint{Lat: 19.1, Lon: 72.9})
	if d < 1100 || d > 1200 {
		t.Fatalf("%v", d)
	}
	// Across the date line is short, not most of the way around the world.
	if d := DistanceKm(GeoPoint{Lat: 0, Lon: 179}, GeoPoint{Lat: 0, Lon: -179}); d > 300 {
		t.Fatalf("%v", d)
	}
}

func TestValidatePlaces(t *testing.T) {
	ok := []domain.GamePlace{{Label: "a", Lat: 1, Lon: 2}, {Label: "b", Lat: 3, Lon: 4}}
	if err := ValidatePlaces(ok, 2, 12); err != nil {
		t.Fatal(err)
	}
	if ValidatePlaces(ok[:1], 2, 12) == nil {
		t.Error("too few")
	}
	if ValidatePlaces([]domain.GamePlace{{Label: "a", Lat: 91}, {Label: "b"}}, 2, 12) == nil {
		t.Error("bad latitude")
	}
}
