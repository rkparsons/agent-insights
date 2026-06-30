package insights

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

func TestSchemaMatchesLiveSkill(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	p := filepath.Join(home, ".claude", "skills", "analyzing-agent-sessions", "schema.json")
	live, err := os.ReadFile(p)
	if err != nil {
		t.Skipf("live skill schema absent: %v", err)
	}
	var a, b any
	if err := json.Unmarshal([]byte(analysisSchema), &a); err != nil {
		t.Fatalf("embedded schema invalid: %v", err)
	}
	if err := json.Unmarshal(live, &b); err != nil {
		t.Fatalf("live schema invalid: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Error("embedded schema drifted from the live skill; update internal/insights/schema.json (and JudgedFields if fields changed)")
	}
}
