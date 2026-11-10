package session

import (
	"testing"

	"deepflow/internal/provider/strictcheck"
)

func TestSchemasMeetStrictRules(t *testing.T) {
	for name, s := range map[string]map[string]any{"consolidate": consolidateSchema, "retrieval": retrievalSchema} {
		if err := strictcheck.Check(s); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
