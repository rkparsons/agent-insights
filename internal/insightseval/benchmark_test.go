package insightseval

import (
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
