package synthesis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

func globalFixture(generatedAt time.Time, model string) insights.GlobalSynthesisJSON {
	return insights.GlobalSynthesisJSON{
		SchemaVersion: 2,
		GeneratedAt:   generatedAt,
		Window:        insights.WindowBoundsJSON{From: "2026-07-27", To: "2026-08-10"},
		Repos: []insights.RepoStatsJSON{
			{Key: "alpha", Window: insights.WindowBoundsJSON{From: "2026-07-27", To: "2026-08-10"}, SessionCount: 12, AnalyzedCount: 12},
		},
		Findings: []insights.FindingJSON{{
			Rank: 1, Title: "t", Statement: "s", RankRationale: "r",
			Asset:          insights.AssetJSON{Type: "habit"},
			EvidenceIDs:    []string{"alpha/F1"},
			AlreadyAdopted: insights.AdoptedJSON{Verdict: "no"},
			Repos:          []string{"alpha"}, SessionCount: 3, LastSeen: "2026-08-06", ActedKey: "abc123",
		}},
		Dropped: []insights.DroppedJSON{},
		Meta:    insights.GlobalMetaJSON{Model: model},
	}
}

func TestStoreGlobalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_INSIGHTS_DIR", dir)
	at := time.Date(2026, 8, 13, 9, 30, 0, 0, time.UTC)

	path, err := StoreGlobal(globalFixture(at, "test-model"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "synthesis", "global", "2026-08-13T09-30-00Z.json")
	if path != want {
		t.Errorf("snapshot path = %q, want %q", path, want)
	}

	got, ok, err := LoadLatestGlobal()
	if err != nil || !ok {
		t.Fatalf("LoadLatestGlobal = (ok=%v, err=%v), want a snapshot", ok, err)
	}
	if got.Meta.Model != "test-model" || !got.GeneratedAt.Equal(at) || len(got.Findings) != 1 {
		t.Errorf("round-tripped snapshot = %+v", got)
	}
}

func TestStoreGlobalRetainsTen(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_INSIGHTS_DIR", dir)
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		if _, err := StoreGlobal(globalFixture(base.AddDate(0, 0, i), "m")); err != nil {
			t.Fatal(err)
		}
	}
	names, err := snapshotJSONNames(filepath.Join(dir, "synthesis", "global"))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != globalRetention {
		t.Fatalf("kept %d snapshots, want %d", len(names), globalRetention)
	}
	if names[0] != "2026-06-03T00-00-00Z.json" {
		t.Errorf("oldest kept = %q, want the 3rd write (the two oldest pruned)", names[0])
	}
}

// TestPreserveFailedSynthesis covers the post-mortem copy: a failed run's
// workdir is deleted with the run, and the model's output is the only artifact
// of a 90-minute spend.
func TestPreserveFailedSynthesis(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_INSIGHTS_DIR", dir)
	workDir := t.TempDir()
	body := []byte(`{"schema_version":2,"findings":[]}`)
	if err := os.WriteFile(filepath.Join(workDir, globalOutputFile), body, 0o644); err != nil {
		t.Fatal(err)
	}

	path, err := preserveFailedSynthesis(workDir, time.Date(2026, 8, 13, 9, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "synthesis", "diagnostics", "2026-08-13T09-30-00Z.json")
	if path != want {
		t.Errorf("diagnostics path = %q, want %q", path, want)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(body) {
		t.Errorf("preserved copy = %q (err %v), want the workdir's synthesis.json verbatim", got, err)
	}
}

// TestPreserveFailedSynthesisNoOutput covers the earlier failure class: the
// CLI died before writing anything, so there is nothing to preserve and that
// must not itself be an error.
func TestPreserveFailedSynthesisNoOutput(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	path, err := preserveFailedSynthesis(t.TempDir(), time.Now().UTC())
	if err != nil || path != "" {
		t.Errorf("got (%q, %v), want (\"\", nil) when the run wrote no output", path, err)
	}
}

// TestStoreGlobalAtomicWrite guards the store against a half-written snapshot
// being served: the file must be complete and parseable the moment it exists.
func TestStoreGlobalAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_INSIGHTS_DIR", dir)
	if _, err := StoreGlobal(globalFixture(time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC), "m")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "synthesis", "global"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("global dir holds %d entries, want exactly the snapshot (no temp files left behind)", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(dir, "synthesis", "global", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var snap insights.GlobalSynthesisJSON
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("stored snapshot does not parse: %v", err)
	}
}
