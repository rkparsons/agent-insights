package synthesis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSchemaMatchesLiveSkill(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	p := filepath.Join(home, ".claude", "skills", "synthesizing-workflow-insights", "schema.json")
	live, err := os.ReadFile(p)
	if err != nil {
		t.Skipf("live skill schema absent: %v", err)
	}
	var a, b any
	if err := json.Unmarshal([]byte(synthesisSchema), &a); err != nil {
		t.Fatalf("embedded schema invalid: %v", err)
	}
	if err := json.Unmarshal(live, &b); err != nil {
		t.Fatalf("live schema invalid: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Error("embedded schema drifted from the live skill; sync src/internal/synthesis/schema.json")
	}
}
