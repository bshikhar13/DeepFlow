package evaluator

import "deepflow/internal/domain"

// Output is one structured evaluation (spec section 5).
type Output struct {
	Intent          Intent           `json:"intent"`
	Concepts        []string         `json:"concepts"`
	Observations    []string         `json:"observations"`
	Inferences      []Inference      `json:"inferences"`
	Evidence        []EvidenceClaim  `json:"evidence"`
	Gaps            []string         `json:"gaps"`
	OpenQuestions   []string         `json:"open_questions"`
	Crystallize     []Crystal        `json:"crystallize"`
	Readiness       string           `json:"readiness"`
	Recommendations []Recommendation `json:"recommendations"`
}

// Intent is what the learner appears to be trying to do.
type Intent struct {
	Summary string `json:"summary"`
	Kind    string `json:"kind"`
	Shifted bool   `json:"shifted"`
}

// Inference is an interpretation, held separately from direct observations.
type Inference struct {
	Claim         string `json:"claim"`
	Confidence    string `json:"confidence"`
	Misconception bool   `json:"misconception"`
}

// EvidenceClaim is the model's proposed evidence, before Guard.
type EvidenceClaim struct {
	Concept       string `json:"concept"`
	Tier          string `json:"tier"`
	Correctness   string `json:"correctness"`
	Reasoning     string `json:"reasoning"`
	Rationale     string `json:"rationale"`
	Uncertainty   string `json:"uncertainty"`
	SourceEventID string `json:"source_event_id"`
}

// Crystal is a Living Document object the model proposes.
type Crystal struct {
	Type       string `json:"type"`
	Content    string `json:"content"`
	Authorship string `json:"authorship"`
}

// Recommendation is a proposed workspace change. The Orchestrator decides.
type Recommendation struct {
	Action     domain.Action   `json:"action"`
	CellID     string          `json:"cell_id"`
	TargetType domain.CellType `json:"target_type"`
	CellType   domain.CellType `json:"cell_type"`
	Objective  string          `json:"objective"`
	Reason     string          `json:"reason"`
	Priority   string          `json:"priority"`
}

func obj(props map[string]any) map[string]any {
	req := make([]string, 0, len(props))
	for k := range props {
		req = append(req, k)
	}
	return map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}
}

func str() map[string]any     { return map[string]any{"type": "string"} }
func boolean() map[string]any { return map[string]any{"type": "boolean"} }
func arr(item map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": item}
}
func enum(v ...string) map[string]any {
	return map[string]any{"type": "string", "enum": v}
}

var cellTypes = []string{"", "dialogue", "concept", "game", "quiz", "journey", "thought", "document", "map"}

var schema = obj(map[string]any{
	"intent": obj(map[string]any{
		"summary": str(), "shifted": boolean(),
		"kind": enum("foundation", "contradiction", "solve", "tangent", "connect", "reconstruct"),
	}),
	"concepts":     arr(str()),
	"observations": arr(str()),
	"inferences": arr(obj(map[string]any{
		"claim": str(), "confidence": enum("low", "medium", "high"), "misconception": boolean(),
	})),
	"evidence": arr(obj(map[string]any{
		"concept": str(), "tier": enum("weak", "moderate", "strong", "durable"),
		"correctness": enum("correct", "partial", "incorrect", "na"),
		"reasoning":   enum("strong", "adequate", "weak", "na"),
		"rationale":   str(), "uncertainty": str(), "source_event_id": str(),
	})),
	"gaps":           arr(str()),
	"open_questions": arr(str()),
	"crystallize": arr(obj(map[string]any{
		"type": enum("insight", "hypothesis", "question", "concept", "connection"), "content": str(),
		"authorship": enum("learner", "ai"),
	})),
	"readiness": enum("explain", "harder", "another_representation", "apply", "reconstruct"),
	"recommendations": arr(obj(map[string]any{
		"action":  enum("UPDATE", "DEEPEN", "SIMPLIFY", "SPAWN", "REPLACE", "RETIRE"),
		"cell_id": str(), "target_type": enum(cellTypes...), "cell_type": enum(cellTypes...),
		"objective": str(), "reason": str(), "priority": enum("low", "medium", "high"),
	})),
})

const system = `You are the Evaluator in DeepFlow, a learning app for school students in classes 6 to 12. You interpret what a learner just did; you never speak to them and you do not control the interface.

Rules:
- Separate observation from inference. "Answered quickly" is an observation; "understands deeply" needs evidence.
- Evidence must rest on what the learner themselves wrote. Reading, viewing, or moving a slider is not understanding.
- Tiers: weak (read or echoed), moderate (answered a familiar question), strong (independently explained and applied to something unfamiliar), durable (reconstructed after a meaningful delay). Default to the lower tier when unsure, and always state uncertainty.
- Score correctness and reasoning separately.
- Set source_event_id to the event the evidence comes from.
- Crystallize only meaningful developments, as a short note of at most 15 words. Use authorship "learner" only for ideas the learner actually expressed.
- Set intent.shifted only when the learner has clearly changed direction.
- Recommend workspace changes sparingly. Prefer UPDATE over structural changes, keep existing cells that still help, and write each reason directly to the learner, as "you", in one short sentence a school student understands. Name a cell by cell_id, or by target_type if unsure. cell_type is the type of the new or resulting cell. Do not recommend changes to the dialogue or journey cells. A "quiz.batch" event summarizes recent quiz results, for spotting patterns: it is not itself evidence, because each answer is already recorded.`
