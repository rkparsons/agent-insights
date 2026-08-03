package insights

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRunBackfillReal exercises the full backfill (real claude -p, subscription auth) over
// a SMALL set of real transcripts when INSIGHTS_REAL_BACKFILL (a glob) is set. It copies
// the matched files into an isolated projects dir so the scan is bounded to them and never
// touches the whole corpus (that run is the maintainer's to trigger). It confirms the window-
// resilient contract end to end: run 1 completes and writes analyses; an identical run 2
// is a no-op with 0 remaining. Manual gate — real subscription calls.
//
//	INSIGHTS_REAL_BACKFILL="$HOME/.claude/projects/<proj>/*.jsonl" \
//	  go test ./internal/insights/ -run TestRunBackfillReal -v -timeout 30m
func TestRunBackfillReal(t *testing.T) {
	glob := os.Getenv("INSIGHTS_REAL_BACKFILL")
	if glob == "" {
		t.Skip("set INSIGHTS_REAL_BACKFILL=<glob of real .jsonl> to run")
	}
	files, err := filepath.Glob(glob)
	if err != nil || len(files) == 0 {
		t.Fatalf("glob %q matched nothing: %v", glob, err)
	}
	if len(files) > 5 {
		t.Fatalf("refusing to run the real judge over %d sessions; narrow the glob to <=5", len(files))
	}

	projects := t.TempDir()
	dst := filepath.Join(projects, "backfill-real")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, filepath.Base(f)), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AGENT_INSIGHTS_PROJECTS_DIR", projects)
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	repo := cfg.Resolver()
	opts := Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: 10 * time.Minute}

	sum, err := RunBackfill(context.Background(), repo, NewClaudeJudge, opts)
	if err != nil {
		t.Fatalf("run1: %v", err)
	}
	if sum.Parked {
		t.Fatalf("run1 parked (usage window hit?): %+v", sum)
	}
	if sum.Analyzed+sum.SkippedGate != len(files) {
		t.Fatalf("run1: analyzed+gated should cover all %d files, got %+v", len(files), sum)
	}
	t.Logf("run1: %+v", sum)

	// Identical re-run: everything is done or gated -> no work, 0 remaining.
	sum2, err := RunBackfill(context.Background(), repo, NewClaudeJudge, opts)
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	if sum2.Analyzed != 0 {
		t.Fatalf("run2 should be a no-op, got %+v", sum2)
	}
	plan, err := BackfillPlan(opts)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ToProcess != 0 {
		t.Fatalf("run2: want 0 to process, got %+v", plan)
	}
	t.Logf("run2 (no-op): %+v; plan=%+v", sum2, plan)
}
