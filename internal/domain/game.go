package domain

// GameSpec is agent-supplied data for one of three fixed game templates. It is
// never code: "target" computes with the safe expression language, while "sort"
// and "locate" are data the app checks itself. It's one flat shape because structured
// outputs handle that more reliably than unions; unused fields stay empty.
type GameSpec struct {
	Kind  string `json:"kind"` // target, sort or locate
	Title string `json:"title"`
	Goal  string `json:"goal"`

	// Hit the target.
	Params []GameParam `json:"params"`
	Output GameOutput  `json:"output"`
	Levels []GameLevel `json:"levels"`
	Note   string      `json:"note"`
	Basis  string      `json:"basis"`

	// Sort it. In "order" mode, Items are listed in the correct order.
	Mode   string     `json:"mode"` // order or groups
	Items  []GameItem `json:"items"`
	Groups []string   `json:"groups"`

	// Find it on the map. The student taps within ToleranceKm of each place.
	Places      []GamePlace `json:"places"`
	ToleranceKm float64     `json:"toleranceKm"`
}

// GameParam is a slider-controlled model input.
type GameParam struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Step  float64 `json:"step"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

// GameOutput is the value the learner steers toward a target.
type GameOutput struct {
	Label string `json:"label"`
	Expr  string `json:"expr"`
	Unit  string `json:"unit"`
}

// GameLevel is one target to reach.
type GameLevel struct {
	Target    float64 `json:"target"`
	Tolerance float64 `json:"tolerance"`
	Hint      string  `json:"hint"`
}

// GameItem is one thing to sort.
type GameItem struct {
	Label string `json:"label"`
	Group string `json:"group"`
}

// GamePlace is a named point on a map. Coordinates come from a model, so the
// app validates their range but shows them as approximate.
type GamePlace struct {
	Label string  `json:"label"`
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
	Fact  string  `json:"fact"`
	When  string  `json:"when"`
}
