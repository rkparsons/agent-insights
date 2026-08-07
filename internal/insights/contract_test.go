package insights_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
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

func TestShowJSONActedKey(t *testing.T) {
	rec := synthesis.Recommendation{Type: "habit", Statement: "Run tests before committing", SessionCount: 3, Quotes: []string{}}
	fixture := synthesis.RepoSynthesis{
		Repo:            "repo-a",
		GeneratedAt:     time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		Window:          synthesis.Window{From: "2026-07-25", To: "2026-08-01", SessionCount: 12, AnalyzedCount: 12},
		Themes:          []synthesis.Theme{{Title: "t", Kind: "friction", Summary: "s", Rank: 1, SessionCount: 5, Quotes: []string{}, SessionIDs: []string{}}},
		Recommendations: []synthesis.Recommendation{rec},
		Meta:            synthesis.Meta{Model: "test-model"},
	}

	show := synthesis.BuildShowJSON([]synthesis.RepoSynthesis{fixture})
	if show.SchemaVersion != insights.ContractVersion {
		t.Fatalf("schema_version = %d, want %d", show.SchemaVersion, insights.ContractVersion)
	}
	if len(show.Syntheses) != 1 || len(show.Syntheses[0].Recommendations) != 1 {
		t.Fatalf("unexpected shape: %+v", show)
	}
	got := show.Syntheses[0].Recommendations[0].ActedKey
	want := synthesis.ActedKey(rec, "repo-a")
	if got == "" || got != want {
		t.Errorf("acted_key = %q, want %q", got, want)
	}
}

// TestShowJSONNeverEmitsNullArrays guards BuildShowJSON's nil-normalization:
// Theme.Quotes/SessionIDs and Recommendation.Quotes/ThemeRefs are all
// zero-valued (nil) here — the shape a real theme/recommendation with zero
// cited quotes or zero evidence refs actually produces — and the contract's
// schema (schemas/show.schema.json) declares quotes/session_ids/theme_refs
// as required arrays, never null.
func TestShowJSONNeverEmitsNullArrays(t *testing.T) {
	fixture := synthesis.RepoSynthesis{
		Repo:        "repo-a",
		GeneratedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		Window:      synthesis.Window{From: "2026-07-25", To: "2026-08-01", SessionCount: 1, AnalyzedCount: 1},
		Themes:      []synthesis.Theme{{Title: "t", Kind: "friction", Summary: "s", Rank: 1, SessionCount: 1}},
		Recommendations: []synthesis.Recommendation{
			{Type: "habit", Statement: "s", SessionCount: 1},
		},
		Meta: synthesis.Meta{Model: "test-model"},
	}

	show := synthesis.BuildShowJSON([]synthesis.RepoSynthesis{fixture})
	data, err := json.Marshal(show)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "null") {
		t.Errorf("expected no null arrays, got: %s", data)
	}

	theme := show.Syntheses[0].Themes[0]
	if theme.Quotes == nil || theme.SessionIDs == nil {
		t.Errorf("theme Quotes/SessionIDs should normalize nil -> [], got %+v", theme)
	}
	rec := show.Syntheses[0].Recommendations[0]
	if rec.Quotes == nil || rec.ThemeRefs == nil {
		t.Errorf("recommendation Quotes/ThemeRefs should normalize nil -> [], got %+v", rec)
	}
}

// TestShowJSONMirrorsSynthesis proves RecommendationJSON/ThemeJSON/WindowJSON/
// MetaJSON carry every exported field of their synthesis-side source type,
// under the same json tag — the load-bearing mirror the contract depends on.
func TestShowJSONMirrorsSynthesis(t *testing.T) {
	assertMirrors(t, reflect.TypeOf(synthesis.Recommendation{}), reflect.TypeOf(insights.RecommendationJSON{}))
	assertMirrors(t, reflect.TypeOf(synthesis.Theme{}), reflect.TypeOf(insights.ThemeJSON{}))
	assertMirrors(t, reflect.TypeOf(synthesis.Window{}), reflect.TypeOf(insights.WindowJSON{}))
	assertMirrors(t, reflect.TypeOf(synthesis.Meta{}), reflect.TypeOf(insights.MetaJSON{}))
	assertMirrors(t, reflect.TypeOf(synthesis.RepoSynthesis{}), reflect.TypeOf(insights.SynthesisJSON{}))
}

// assertMirrors checks every exported field of src has a same-named,
// same-json-tag counterpart in dst. dst may carry extra fields (e.g.
// RecommendationJSON.ActedKey) not present on src.
func assertMirrors(t *testing.T, src, dst reflect.Type) {
	t.Helper()
	for i := 0; i < src.NumField(); i++ {
		sf := src.Field(i)
		if sf.PkgPath != "" { // unexported
			continue
		}
		df, ok := dst.FieldByName(sf.Name)
		if !ok {
			t.Errorf("%s.%s has no counterpart field in %s", src.Name(), sf.Name, dst.Name())
			continue
		}
		if sf.Tag.Get("json") != df.Tag.Get("json") {
			t.Errorf("%s.%s json tag %q != %s.%s json tag %q", src.Name(), sf.Name, sf.Tag.Get("json"), dst.Name(), df.Name, df.Tag.Get("json"))
		}
	}
}
