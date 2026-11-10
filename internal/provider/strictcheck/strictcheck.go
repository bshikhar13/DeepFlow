// Package strictcheck verifies, in tests, that a JSON schema meets the rules of
// OpenAI's strict structured outputs, which rejects a bad schema on every call.
package strictcheck

import "fmt"

// Check returns the first rule the schema breaks.
func Check(schema map[string]any) error { return walk("$", schema) }

func walk(path string, s map[string]any) error {
	t, _ := s["type"].(string)
	if path == "$" && t != "object" {
		return fmt.Errorf("%s: the root must be an object", path)
	}
	for _, banned := range []string{"allOf", "not", "dependentRequired", "if", "then", "else", "$ref"} {
		if _, ok := s[banned]; ok {
			return fmt.Errorf("%s: %s isn't supported", path, banned)
		}
	}
	switch t {
	case "object":
		if s["additionalProperties"] != false {
			return fmt.Errorf("%s: additionalProperties must be false", path)
		}
		props, _ := s["properties"].(map[string]any)
		required := map[string]bool{}
		list, ok := s["required"].([]string)
		if !ok {
			return fmt.Errorf("%s: required is missing", path)
		}
		for _, r := range list {
			required[r] = true
		}
		for name, v := range props {
			if !required[name] {
				return fmt.Errorf("%s.%s: every property must be required", path, name)
			}
			child, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s: not a schema", path, name)
			}
			if err := walk(path+"."+name, child); err != nil {
				return err
			}
		}
	case "array":
		items, ok := s["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: items is missing", path)
		}
		return walk(path+"[]", items)
	case "string", "number", "integer", "boolean", "null":
	default:
		return fmt.Errorf("%s: unsupported type %q", path, t)
	}
	return nil
}
