package insights

import (
	"encoding/json"
	"testing"
)

func TestEmbeddedSchemaValid(t *testing.T) {
	if analysisSchema == "" {
		t.Fatal("embedded schema is empty")
	}
	var v any
	if err := json.Unmarshal([]byte(analysisSchema), &v); err != nil {
		t.Fatalf("embedded schema is not valid JSON: %v", err)
	}
}
