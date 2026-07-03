package insightseval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"tmux-ctrl/internal/insights"
	"tmux-ctrl/internal/synthesis"
)

func analysisFor(id, repo, cwd string, start time.Time) insights.AgentSessionAnalysis {
	return insights.AgentSessionAnalysis{Stats: insights.AgentSessionStats{
		SessionID: id, Repo: repo, Cwd: cwd, Start: start,
	}}
}

func TestBuildBenchmarkPopulations(t *testing.T) {
	gen := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return time.Date(2026, 6, d, 9, 0, 0, 0, time.UTC) }
	analyses := []insights.AgentSessionAnalysis{
		analysisFor("a1", "/Users/x/Developer/myrepo", "", day(24)),
		analysisFor("a2", "/Users/x/Developer/myrepo", "", day(26)),
		// meta: cwd mentions insights → excluded from scoring only
		analysisFor("a3", "/Users/x/Developer/myrepo/.worktrees/insights-generation",
			"/Users/x/Developer/myrepo/.worktrees/insights-generation", day(28)),
		// other repo: not in this bucket
		analysisFor("b1", "/Users/x/Developer/other", "", day(25)),
	}
	truths := map[string]synthesis.RepoSynthesis{
		"myrepo": {GeneratedAt: gen,
			// reversed From/To, as the pre-sort-fix reports print
			Window: synthesis.Window{From: "2026-06-28", To: "2026-06-24", AnalyzedCount: 3}},
	}
	b, problems := BuildBenchmark(gen, analyses, truths)
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	bp := b.Buckets["myrepo"]
	if !slices.Equal(bp.AsConsumed, []string{"a1", "a2", "a3"}) {
		t.Fatalf("as_consumed = %v", bp.AsConsumed)
	}
	if !slices.Equal(bp.Scoring, []string{"a1", "a2"}) {
		t.Fatalf("scoring = %v", bp.Scoring)
	}
	if !bp.Resolved || bp.ReconstructedCount != 3 || bp.ExpectedAnalyzed != 3 {
		t.Fatalf("resolution: %+v", bp)
	}
	if bp.WindowFrom != "2026-06-24" || bp.WindowTo != "2026-06-28" {
		t.Fatalf("window not normalized: %+v", bp)
	}
	if bp.RepoPath != "/Users/x/Developer/myrepo" {
		t.Fatalf("repo path: %q", bp.RepoPath)
	}
}

func TestBuildBenchmarkPostGenerationFallback(t *testing.T) {
	gen := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	analyses := []insights.AgentSessionAnalysis{
		analysisFor("a1", "/Users/x/Developer/myrepo", "", time.Date(2026, 6, 24, 9, 0, 0, 0, time.UTC)),
		// started after the report was generated → excluded by the fallback
		analysisFor("a9", "/Users/x/Developer/myrepo", "", time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC)),
	}
	truths := map[string]synthesis.RepoSynthesis{
		"myrepo": {GeneratedAt: gen, Window: synthesis.Window{From: "2026-06-24", To: "2026-06-24", AnalyzedCount: 1}},
	}
	b, problems := BuildBenchmark(gen, analyses, truths)
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if got := b.Buckets["myrepo"].AsConsumed; !slices.Equal(got, []string{"a1"}) {
		t.Fatalf("as_consumed = %v", got)
	}
}

func TestBuildBenchmarkUnresolvedMismatch(t *testing.T) {
	gen := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	analyses := []insights.AgentSessionAnalysis{
		analysisFor("a1", "/Users/x/Developer/myrepo", "", time.Date(2026, 6, 24, 9, 0, 0, 0, time.UTC)),
	}
	truths := map[string]synthesis.RepoSynthesis{
		"myrepo": {GeneratedAt: gen, Window: synthesis.Window{From: "2026-06-24", To: "2026-06-24", AnalyzedCount: 5}},
	}
	b, problems := BuildBenchmark(gen, analyses, truths)
	if len(problems) != 1 {
		t.Fatalf("problems = %v", problems)
	}
	if b.Buckets["myrepo"].Resolved {
		t.Fatal("bucket should be unresolved")
	}
}

func TestLoadGroundTruth(t *testing.T) {
	dir := t.TempDir()

	// Create myrepo dir with two JSON files (newest and older)
	myrepoDir := filepath.Join(dir, "myrepo")
	if err := os.MkdirAll(myrepoDir, 0755); err != nil {
		t.Fatalf("mkdir myrepo: %v", err)
	}

	// 2026-07-01: AnalyzedCount = 5
	older := synthesis.RepoSynthesis{
		Repo:        "myrepo",
		GeneratedAt: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		Window:      synthesis.Window{From: "2026-06-24", To: "2026-06-30", AnalyzedCount: 5},
		Themes:      []synthesis.Theme{},
		Meta:        synthesis.Meta{Model: "test"},
	}
	olderJSON, err := json.Marshal(older)
	if err != nil {
		t.Fatalf("marshal older: %v", err)
	}
	if err := os.WriteFile(filepath.Join(myrepoDir, "2026-07-01.json"), olderJSON, 0644); err != nil {
		t.Fatalf("write 2026-07-01.json: %v", err)
	}

	// 2026-07-02: AnalyzedCount = 10 (should be selected as newest)
	newer := synthesis.RepoSynthesis{
		Repo:        "myrepo",
		GeneratedAt: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		Window:      synthesis.Window{From: "2026-06-24", To: "2026-07-01", AnalyzedCount: 10},
		Themes:      []synthesis.Theme{},
		Meta:        synthesis.Meta{Model: "test"},
	}
	newerJSON, err := json.Marshal(newer)
	if err != nil {
		t.Fatalf("marshal newer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(myrepoDir, "2026-07-02.json"), newerJSON, 0644); err != nil {
		t.Fatalf("write 2026-07-02.json: %v", err)
	}

	// Create other dir: invalid JSON (should skip) and valid JSON (should use)
	otherDir := filepath.Join(dir, "other")
	if err := os.MkdirAll(otherDir, 0755); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}

	// 2026-07-03: invalid JSON (should be skipped)
	if err := os.WriteFile(filepath.Join(otherDir, "2026-07-03.json"), []byte("{not json"), 0644); err != nil {
		t.Fatalf("write invalid JSON: %v", err)
	}

	// 2026-07-01: valid JSON (should be selected as fallback)
	otherValid := synthesis.RepoSynthesis{
		Repo:        "other",
		GeneratedAt: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		Window:      synthesis.Window{From: "2026-06-20", To: "2026-06-30", AnalyzedCount: 3},
		Themes:      []synthesis.Theme{},
		Meta:        synthesis.Meta{Model: "test"},
	}
	otherValidJSON, err := json.Marshal(otherValid)
	if err != nil {
		t.Fatalf("marshal other valid: %v", err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "2026-07-01.json"), otherValidJSON, 0644); err != nil {
		t.Fatalf("write other 2026-07-01.json: %v", err)
	}

	// Create stray README.md at root (should be ignored)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test"), 0644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	// Load and verify
	got, err := loadGroundTruth(dir)
	if err != nil {
		t.Fatalf("loadGroundTruth: %v", err)
	}

	// Verify myrepo: should have newer (2026-07-02, AnalyzedCount=10)
	if _, ok := got["myrepo"]; !ok {
		t.Fatal("myrepo not in result")
	}
	if got["myrepo"].Window.AnalyzedCount != 10 {
		t.Fatalf("myrepo AnalyzedCount = %d, want 10", got["myrepo"].Window.AnalyzedCount)
	}

	// Verify other: should have valid (2026-07-01, AnalyzedCount=3)
	if _, ok := got["other"]; !ok {
		t.Fatal("other not in result")
	}
	if got["other"].Window.AnalyzedCount != 3 {
		t.Fatalf("other AnalyzedCount = %d, want 3", got["other"].Window.AnalyzedCount)
	}

	// Verify only two entries
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(got), got)
	}
}
