package insights_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/rkparsons/agent-insights/internal/insights"
)

func TestStatusJSONShape(t *testing.T) {
	lastRun := &insights.LastRunJSON{StartedAt: "2026-08-01T00:00:00Z", FinishedAt: "2026-08-01T00:05:00Z"}
	status := insights.BuildStatus("/store", "/store/logs/synthesize-2026-08-03.log", true, "", []string{"repo-a"}, []string{"key1"}, lastRun)
	if status.SchemaVersion != insights.ContractVersion {
		t.Fatalf("schema_version = %d, want %d", status.SchemaVersion, insights.ContractVersion)
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	want := []string{"schema_version", "store_root", "log_path", "running", "due_repos", "acted_keys", "last_run"}
	assertKeySet(t, m, want)
}

// TestStatusJSONOmitsLastRunWhenNil guards the omitempty on last_run, and
// that nil due_repos/acted_keys inputs still marshal as [] (never null).
func TestStatusJSONOmitsLastRunWhenNil(t *testing.T) {
	status := insights.BuildStatus("/store", "/store/logs/x.log", false, "", nil, nil, nil)
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["last_run"]; ok {
		t.Fatalf("last_run present when nil: %s", data)
	}
	assertKeySet(t, m, []string{"schema_version", "store_root", "log_path", "running", "due_repos", "acted_keys"})
	if arr, ok := m["due_repos"].([]any); !ok || arr == nil {
		t.Fatalf("due_repos should be an empty array, not null: %s", data)
	}
}

func TestBuildStatusRunningOp(t *testing.T) {
	s := insights.BuildStatus("root", "log", true, "analyze", nil, nil, nil)
	if s.RunningOp != "analyze" {
		t.Errorf("RunningOp = %q, want analyze", s.RunningOp)
	}
	s = insights.BuildStatus("root", "log", false, "analyze", nil, nil, nil)
	if s.RunningOp != "" {
		t.Errorf("RunningOp with running=false = %q, want empty", s.RunningOp)
	}
}

func assertKeySet(t *testing.T, m map[string]any, want []string) {
	t.Helper()
	got := make([]string, 0, len(m))
	for k := range m {
		got = append(got, k)
	}
	sort.Strings(got)
	wantSorted := append([]string(nil), want...)
	sort.Strings(wantSorted)
	if !reflect.DeepEqual(got, wantSorted) {
		t.Fatalf("keys = %v, want %v", got, wantSorted)
	}
}
