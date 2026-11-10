package evaluator

import (
	"testing"

	"deepflow/internal/provider/strictcheck"
)

func TestSchemaMeetsStrictRules(t *testing.T) {
	if err := strictcheck.Check(schema); err != nil {
		t.Fatal(err)
	}
}
