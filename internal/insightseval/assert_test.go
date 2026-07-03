package insightseval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func writeAnalysisStub(t *testing.T, dir, id string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "analyses"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"transcript_mtime": mtime})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "analyses", id+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAssertFrozenFindsGapsAndSkews(t *testing.T) {
	insightsDir := t.TempDir()
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", insightsDir)
	mt := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	writeAnalysisStub(t, insightsDir, "ok", mt)
	writeAnalysisStub(t, insightsDir, "skewed", mt)

	b := Benchmark{Buckets: map[string]BucketPopulations{
		"myrepo": {AsConsumed: []string{"ok", "skewed", "missing"}},
	}}
	m := Manifest{Entries: []ManifestEntry{
		{SessionID: "ok", Mtime: mt},
		{SessionID: "skewed", Mtime: mt.Add(time.Hour)}, // transcript grew after analysis
	}}
	iss := AssertFrozen(b, m, []string{"myrepo: reconstructed 3 analyses, report says 4"})
	if !slices.Equal(iss.Gaps, []string{"myrepo/missing"}) {
		t.Fatalf("gaps = %v", iss.Gaps)
	}
	if !slices.Equal(iss.Skews, []string{"myrepo/skewed"}) {
		t.Fatalf("skews = %v", iss.Skews)
	}
	if iss.Clean() {
		t.Fatal("issues present but Clean() = true")
	}
	clean := AssertFrozen(Benchmark{Buckets: map[string]BucketPopulations{
		"myrepo": {AsConsumed: []string{"ok"}},
	}}, Manifest{Entries: []ManifestEntry{{SessionID: "ok", Mtime: mt}}}, nil)
	if !clean.Clean() {
		t.Fatalf("want clean, got %+v", clean)
	}
}
