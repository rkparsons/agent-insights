package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
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
		analysisFor("a1", "/Users/dev/Developer/myrepo", "", day(24)),
		analysisFor("a2", "/Users/dev/Developer/myrepo", "", day(26)),
		// meta: cwd mentions insights → excluded from scoring only
		analysisFor("a3", "/Users/dev/Developer/myrepo/.worktrees/insights-generation",
			"/Users/dev/Developer/myrepo/.worktrees/insights-generation", day(28)),
		// other repo: not in this bucket
		analysisFor("b1", "/Users/dev/Developer/other", "", day(25)),
	}
	truths := map[string]RepoSynthesis{
		"myrepo": {GeneratedAt: gen,
			// reversed From/To, as the pre-sort-fix reports print
			Window: Window{From: "2026-06-28", To: "2026-06-24", AnalyzedCount: 3}},
	}
	b, problems := BuildBenchmark(gen, analyses, truths, nil, insights.Config{})
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
	if bp.RepoPath != "/Users/dev/Developer/myrepo" {
		t.Fatalf("repo path: %q", bp.RepoPath)
	}
}

// TestBuildBenchmarkRepoPathStripsWorktree covers finding H: a worktree
// checkout's path must not land in benchmark.json's RepoPath.
func TestBuildBenchmarkRepoPathStripsWorktree(t *testing.T) {
	gen := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	analyses := []insights.AgentSessionAnalysis{
		analysisFor("a1", "/Users/dev/Developer/myrepo/.worktrees/some-feature", "",
			time.Date(2026, 6, 24, 9, 0, 0, 0, time.UTC)),
	}
	truths := map[string]RepoSynthesis{
		"myrepo": {GeneratedAt: gen, Window: Window{From: "2026-06-24", To: "2026-06-24", AnalyzedCount: 1}},
	}
	b, problems := BuildBenchmark(gen, analyses, truths, nil, insights.Config{})
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if got := b.Buckets["myrepo"].RepoPath; got != "/Users/dev/Developer/myrepo" {
		t.Fatalf("RepoPath = %q, want worktree segment stripped", got)
	}
}

func TestBuildBenchmarkPostGenerationFallback(t *testing.T) {
	gen := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	analyses := []insights.AgentSessionAnalysis{
		analysisFor("a1", "/Users/dev/Developer/myrepo", "", time.Date(2026, 6, 24, 9, 0, 0, 0, time.UTC)),
		// started after the report was generated → excluded by the fallback
		analysisFor("a9", "/Users/dev/Developer/myrepo", "", time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC)),
	}
	truths := map[string]RepoSynthesis{
		"myrepo": {GeneratedAt: gen, Window: Window{From: "2026-06-24", To: "2026-06-24", AnalyzedCount: 1}},
	}
	b, problems := BuildBenchmark(gen, analyses, truths, nil, insights.Config{})
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
		analysisFor("a1", "/Users/dev/Developer/myrepo", "", time.Date(2026, 6, 24, 9, 0, 0, 0, time.UTC)),
	}
	truths := map[string]RepoSynthesis{
		"myrepo": {GeneratedAt: gen, Window: Window{From: "2026-06-24", To: "2026-06-24", AnalyzedCount: 5}},
	}
	b, problems := BuildBenchmark(gen, analyses, truths, nil, insights.Config{})
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
	older := RepoSynthesis{
		Repo:        "myrepo",
		GeneratedAt: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		Window:      Window{From: "2026-06-24", To: "2026-06-30", AnalyzedCount: 5},
		Themes:      []Theme{},
		Meta:        Meta{Model: "test"},
	}
	olderJSON, err := json.Marshal(older)
	if err != nil {
		t.Fatalf("marshal older: %v", err)
	}
	if err := os.WriteFile(filepath.Join(myrepoDir, "2026-07-01.json"), olderJSON, 0644); err != nil {
		t.Fatalf("write 2026-07-01.json: %v", err)
	}

	// 2026-07-02: AnalyzedCount = 10 (should be selected as newest)
	newer := RepoSynthesis{
		Repo:        "myrepo",
		GeneratedAt: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		Window:      Window{From: "2026-06-24", To: "2026-07-01", AnalyzedCount: 10},
		Themes:      []Theme{},
		Meta:        Meta{Model: "test"},
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
	otherValid := RepoSynthesis{
		Repo:        "other",
		GeneratedAt: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		Window:      Window{From: "2026-06-20", To: "2026-06-30", AnalyzedCount: 3},
		Themes:      []Theme{},
		Meta:        Meta{Model: "test"},
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

// The v2 global snapshot dir is ground truth, not a repo bucket: loading the
// v1 (per-repo, theme-shaped) truths must skip it, and the v2 loader must pick
// the newest snapshot out of it.
func TestGroundTruthSeparatesGlobalFromRepoDirs(t *testing.T) {
	dir := t.TempDir()
	old := insights.GlobalSynthesisJSON{SchemaVersion: 2,
		GeneratedAt: time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC),
		Repos:       []insights.RepoStatsJSON{{Key: "alpha", AnalyzedCount: 1}}}
	newer := insights.GlobalSynthesisJSON{SchemaVersion: 2,
		GeneratedAt: time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC),
		Repos: []insights.RepoStatsJSON{
			{Key: "alpha", Window: insights.WindowBoundsJSON{From: "2026-08-01", To: "2026-08-10"}, AnalyzedCount: 2},
			{Key: "beta", Window: insights.WindowBoundsJSON{From: "2026-08-02", To: "2026-08-09"}, AnalyzedCount: 1},
		}}
	for _, s := range []insights.GlobalSynthesisJSON{old, newer} {
		name := s.GeneratedAt.Format("2006-01-02T15-04-05Z") + ".json"
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, synthesis.GlobalDirName), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, synthesis.GlobalDirName, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// a v1 repo dir beside it stays readable (historical records)
	v1 := RepoSynthesis{Repo: "alpha", GeneratedAt: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		Window: Window{From: "2026-06-24", To: "2026-07-01", AnalyzedCount: 4}}
	raw, err := json.Marshal(v1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "2026-07-02.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	truths, err := loadGroundTruth(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, phantom := truths["global"]; phantom {
		t.Fatalf("the v2 snapshot dir must never card as a repo bucket: %v", truths)
	}
	if len(truths) != 1 || truths["alpha"].Window.AnalyzedCount != 4 {
		t.Fatalf("v1 ground truth must stay readable: %+v", truths)
	}

	global, ok, err := loadGlobalGroundTruth(dir)
	if err != nil || !ok {
		t.Fatalf("global ground truth: ok=%v err=%v", ok, err)
	}
	if len(global.Repos) != 2 || !global.GeneratedAt.Equal(newer.GeneratedAt) {
		t.Fatalf("newest snapshot wins: %+v", global)
	}
}

// A v2 freeze reconstructs its buckets from the global snapshot's per-repo
// stats — one bucket per repo the snapshot names, not one per ground-truth dir.
func TestBuildBenchmarkFromGlobalSnapshot(t *testing.T) {
	gen := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return time.Date(2026, 8, d, 9, 0, 0, 0, time.UTC) }
	analyses := []insights.AgentSessionAnalysis{
		analysisFor("a1", "/Users/dev/Developer/alpha", "", day(2)),
		analysisFor("a2", "/Users/dev/Developer/alpha/.worktrees/wt", "", day(3)),
		analysisFor("b1", "/Users/dev/Developer/beta", "", day(4)),
		// meta session: excluded from scoring, kept in as_consumed
		analysisFor("b2", "/Users/dev/Developer/beta/.worktrees/insights-generation",
			"/Users/dev/Developer/beta/.worktrees/insights-generation", day(5)),
	}
	global := insights.GlobalSynthesisJSON{SchemaVersion: 2, GeneratedAt: gen,
		Repos: []insights.RepoStatsJSON{
			{Key: "alpha", Window: insights.WindowBoundsJSON{From: "2026-08-03", To: "2026-08-02"}, AnalyzedCount: 2},
			{Key: "beta", Window: insights.WindowBoundsJSON{From: "2026-08-04", To: "2026-08-05"}, AnalyzedCount: 2},
		}}
	b, problems := BuildBenchmark(gen, analyses, nil, &global, insights.Config{})
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if len(b.Buckets) != 2 {
		t.Fatalf("buckets: %+v", b.Buckets)
	}
	alpha := b.Buckets["alpha"]
	if !slices.Equal(alpha.AsConsumed, []string{"a1", "a2"}) || !alpha.Resolved {
		t.Fatalf("alpha: %+v", alpha)
	}
	if alpha.RepoPath != "/Users/dev/Developer/alpha" {
		t.Fatalf("worktree path must be stripped: %q", alpha.RepoPath)
	}
	if alpha.WindowFrom != "2026-08-02" || alpha.WindowTo != "2026-08-03" {
		t.Fatalf("window not normalized: %+v", alpha)
	}
	beta := b.Buckets["beta"]
	if !slices.Equal(beta.AsConsumed, []string{"b1", "b2"}) || !slices.Equal(beta.Scoring, []string{"b1"}) {
		t.Fatalf("beta populations: %+v", beta)
	}
}

// TestLoadGroundTruthAllMalformedDirErrors covers finding H: a repo dir whose
// every .json file is malformed must return an error naming the dir, not
// silently omit the bucket.
func TestLoadGroundTruthAllMalformedDirErrors(t *testing.T) {
	dir := t.TempDir()
	badDir := filepath.Join(dir, "badrepo")
	if err := os.MkdirAll(badDir, 0755); err != nil {
		t.Fatalf("mkdir badrepo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "2026-07-01.json"), []byte("{not json"), 0644); err != nil {
		t.Fatalf("write malformed json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "2026-07-02.json"), []byte("also not json"), 0644); err != nil {
		t.Fatalf("write malformed json: %v", err)
	}

	_, err := loadGroundTruth(dir)
	if err == nil {
		t.Fatal("want error for a repo dir with every json malformed, got nil")
	}
	if !strings.Contains(err.Error(), "badrepo") {
		t.Fatalf("error must name the dir, got: %v", err)
	}
}

func TestLoadBenchmarkRoundTrip(t *testing.T) {
	dir := t.TempDir()
	_, ok, err := loadBenchmark(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("no benchmark.json yet; ok must be false")
	}

	want := Benchmark{
		FrozenAt: time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
		Buckets: map[string]BucketPopulations{
			"myrepo": {AsConsumed: []string{"a1"}, Resolved: true},
		},
	}
	if err := writeJSON(filepath.Join(dir, "benchmark.json"), want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := loadBenchmark(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("want ok=true once benchmark.json exists")
	}
	if !got.FrozenAt.Equal(want.FrozenAt) || !slices.Equal(got.Buckets["myrepo"].AsConsumed, want.Buckets["myrepo"].AsConsumed) {
		t.Fatalf("loaded benchmark = %+v, want %+v", got, want)
	}
}

func TestAllBucketsResolved(t *testing.T) {
	resolved := Benchmark{Buckets: map[string]BucketPopulations{
		"a": {Resolved: true}, "b": {Resolved: true},
	}}
	if !allBucketsResolved(resolved) {
		t.Fatal("want true when every bucket is resolved")
	}
	mixed := Benchmark{Buckets: map[string]BucketPopulations{
		"a": {Resolved: true}, "b": {Resolved: false},
	}}
	if allBucketsResolved(mixed) {
		t.Fatal("want false when any bucket is unresolved")
	}
	if !allBucketsResolved(Benchmark{}) {
		t.Fatal("want true (vacuous) for an empty bucket set")
	}
}
