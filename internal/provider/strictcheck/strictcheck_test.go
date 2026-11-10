package strictcheck

import "testing"

func TestCheckCatchesViolations(t *testing.T) {
	str := map[string]any{"type": "string"}
	good := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"a"}, "properties": map[string]any{"a": str}}
	if err := Check(good); err != nil {
		t.Fatal(err)
	}
	bad := map[string]map[string]any{
		"open object":      {"type": "object", "required": []string{"a"}, "properties": map[string]any{"a": str}},
		"optional field":   {"type": "object", "additionalProperties": false, "required": []string{}, "properties": map[string]any{"a": str}},
		"root not object":  {"type": "array", "items": str},
		"array no items":   {"type": "object", "additionalProperties": false, "required": []string{"a"}, "properties": map[string]any{"a": map[string]any{"type": "array"}}},
		"nested violation": {"type": "object", "additionalProperties": false, "required": []string{"a"}, "properties": map[string]any{"a": map[string]any{"type": "object", "properties": map[string]any{}}}},
	}
	for name, s := range bad {
		if Check(s) == nil {
			t.Errorf("%s: should fail", name)
		}
	}
}
