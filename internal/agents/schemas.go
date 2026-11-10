package agents

func obj(props map[string]any) map[string]any {
	req := make([]string, 0, len(props))
	for k := range props {
		req = append(req, k)
	}
	return map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}
}

func str() map[string]any { return map[string]any{"type": "string"} }
func num() map[string]any { return map[string]any{"type": "number"} }
func arr(item map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": item}
}

var placeSchema = obj(map[string]any{"label": str(), "lat": num(), "lon": num(), "fact": str(), "when": str()})

func boolean() map[string]any { return map[string]any{"type": "boolean"} }

func integer() map[string]any { return map[string]any{"type": "integer"} }
func enum(v ...string) map[string]any {
	return map[string]any{"type": "string", "enum": v}
}

var conceptSchema = obj(map[string]any{"title": str(), "idea": str(), "example": str(), "emoji": str(), "related": arr(str())})

var quizSchema = obj(map[string]any{"questions": arr(obj(map[string]any{
	"prompt": str(), "options": arr(str()), "answer": integer(), "why": str(),
}))})

var gameSchema = obj(map[string]any{
	"kind": enum("target", "sort", "locate"), "title": str(), "goal": str(),
	"params": arr(obj(map[string]any{"id": str(), "label": str(), "min": num(), "max": num(), "step": num(), "value": num(), "unit": str()})),
	"output": obj(map[string]any{"label": str(), "expr": str(), "unit": str()}),
	"levels": arr(obj(map[string]any{"target": num(), "tolerance": num(), "hint": str()})),
	"note":   str(), "basis": enum("illustrative", "none"),
	"mode":   enum("order", "groups", "none"),
	"items":  arr(obj(map[string]any{"label": str(), "group": str()})),
	"groups": arr(str()),
	"places": arr(placeSchema), "toleranceKm": num(),
})

var mapSchema = obj(map[string]any{"title": str(), "places": arr(placeSchema), "connect": boolean()})

var journeySchema = obj(map[string]any{"topics": arr(obj(map[string]any{"title": str(), "milestone": str()})), "uses_maps": boolean()})

var thoughtSchema = obj(map[string]any{"scenario": str(), "question": str(), "reveal": str()})
var thoughtRespondSchema = obj(map[string]any{"feedback": str()})
