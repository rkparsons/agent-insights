package insightseval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"tmux-ctrl/internal/insights"
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
		"myrepo": {AsConsumed: []string{"ok", "skewed", "missing", "unanalyzed"}},
	}}
	m := Manifest{Entries: []ManifestEntry{
		{SessionID: "ok", Mtime: mt},
		{SessionID: "skewed", Mtime: mt.Add(time.Hour)}, // transcript grew after analysis
		{SessionID: "unanalyzed", Mtime: mt},            // has manifest entry but no analysis stub
	}}
	iss := AssertFrozen(b, m, []string{"myrepo: reconstructed 3 analyses, report says 4"}, insights.ReadAnalysisMtime)
	if !slices.Equal(iss.Gaps, []string{"myrepo/missing"}) {
		t.Fatalf("gaps = %v", iss.Gaps)
	}
	if !slices.Equal(iss.Skews, []string{"myrepo/skewed"}) {
		t.Fatalf("skews = %v", iss.Skews)
	}
	// "unanalyzed" has manifest entry but no analysis stub; this is not a skew.
	for _, id := range []string{"unanalyzed"} {
		if slices.Contains(iss.Gaps, "myrepo/"+id) || slices.Contains(iss.Skews, "myrepo/"+id) {
			t.Fatalf("unanalyzed should not appear in gaps or skews, got gaps=%v skews=%v", iss.Gaps, iss.Skews)
		}
	}
	if iss.Clean() {
		t.Fatal("issues present but Clean() = true")
	}
	clean := AssertFrozen(Benchmark{Buckets: map[string]BucketPopulations{
		"myrepo": {AsConsumed: []string{"ok"}},
	}}, Manifest{Entries: []ManifestEntry{{SessionID: "ok", Mtime: mt}}}, nil, insights.ReadAnalysisMtime)
	if !clean.Clean() {
		t.Fatalf("want clean, got %+v", clean)
	}
}

// TestAssertFrozenUsesProvidedPoolMtimeLookup covers finding F: AssertFrozen
// must consult whatever poolMtime lookup the caller threads in, so RunFreeze
// can point it at baseline-pool/v1 once that's canonical instead of the live
// analyses pool.
func TestAssertFrozenUsesProvidedPoolMtimeLookup(t *testing.T) {
	mt := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	b := Benchmark{Buckets: map[string]BucketPopulations{
		"myrepo": {AsConsumed: []string{"s1"}},
	}}
	m := Manifest{Entries: []ManifestEntry{{SessionID: "s1", Mtime: mt}}}

	skewed := AssertFrozen(b, m, nil, func(id string) (time.Time, bool) {
		return mt.Add(time.Hour), true
	})
	if !slices.Equal(skewed.Skews, []string{"myrepo/s1"}) {
		t.Fatalf("skews = %v, want myrepo/s1", skewed.Skews)
	}

	clean := AssertFrozen(b, m, nil, func(id string) (time.Time, bool) {
		return mt, true
	})
	if !clean.Clean() {
		t.Fatalf("want clean, got %+v", clean)
	}
}

func TestReadStampedMtime(t *testing.T) {
	dir := t.TempDir()
	mt := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	raw, err := json.Marshal(map[string]any{"transcript_mtime": mt})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "s1.json")
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := readStampedMtime(p)
	if !ok || !got.Equal(mt) {
		t.Fatalf("readStampedMtime = %v, %v, want %v, true", got, ok, mt)
	}

	if _, ok := readStampedMtime(filepath.Join(dir, "missing.json")); ok {
		t.Fatal("missing file must return ok=false")
	}
}

func TestFreezeIssuesBlocking(t *testing.T) {
	cases := []struct {
		name    string
		issues  FreezeIssues
		blocked bool
	}{
		{"none", FreezeIssues{}, false},
		{"gaps only", FreezeIssues{Gaps: []string{"myrepo/pruned"}}, false},
		{"skews", FreezeIssues{Skews: []string{"myrepo/skewed"}}, true},
		{"count mismatches", FreezeIssues{CountMismatches: []string{"myrepo: mismatch"}}, true},
		{"gaps and skews", FreezeIssues{Gaps: []string{"myrepo/pruned"}, Skews: []string{"myrepo/skewed"}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.issues.Blocking(); got != c.blocked {
				t.Fatalf("Blocking() = %v, want %v", got, c.blocked)
			}
		})
	}
	// gaps-only is not Clean() (Clean still requires all three empty) even
	// though it is non-blocking.
	if (FreezeIssues{Gaps: []string{"myrepo/pruned"}}).Clean() {
		t.Fatal("gaps-only issues must not be Clean()")
	}
}
