package agents

import (
	"testing"

	"deepflow/internal/provider/strictcheck"
)

func TestSchemasMeetStrictRules(t *testing.T) {
	for name, s := range map[string]map[string]any{
		"concept": conceptSchema, "game": gameSchema, "quiz": quizSchema, "journey": journeySchema,
		"map": mapSchema, "thought": thoughtSchema, "thoughtRespond": thoughtRespondSchema,
	} {
		if err := strictcheck.Check(s); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
