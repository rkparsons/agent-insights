package insights

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tmux-ctrl/internal/transcript"
)

func TestRunBackfillGatesAndRecords(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	writeSession(t, projects, "proj", "big", 6)   // substantial -> analyzed
	writeSession(t, projects, "proj", "small", 2) // trivial -> gated
	// a subagent transcript must be ignored entirely
	writeSession(t, projects, "proj/subagents", "agent-zzz", 9)

	judge := fakeJudge{fields: substantialJudged()}
	opts := Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute}
	sum, err := RunBackfill(context.Background(), noRepo, fixedJudge(judge), opts)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Analyzed != 1 || sum.SkippedGate != 1 {
		t.Errorf("want 1 analyzed + 1 gated, got %+v", sum)
	}
	if _, ok := ReadAnalysisMtime("big"); !ok {
		t.Error("big should be analyzed")
	}
	if _, ok := ReadAnalysisMtime("small"); ok {
		t.Error("small should be gated, not analyzed")
	}
	m, _ := loadManifest()
	if e, ok := m["small"]; !ok || e.Outcome != "gated" || e.Threshold != DefaultMinAssistantTurns {
		t.Errorf("gated entry missing/wrong: %+v ok=%v", e, ok)
	}
}

func TestRunBackfillResumeSkips(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	writeSession(t, projects, "proj", "big", 6)
	writeSession(t, projects, "proj", "small", 2)
	judge := fakeJudge{fields: substantialJudged()}
	opts := Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute}

	if _, err := RunBackfill(context.Background(), noRepo, fixedJudge(judge), opts); err != nil {
		t.Fatal(err)
	}
	sum, err := RunBackfill(context.Background(), noRepo, fixedJudge(judge), opts)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Analyzed != 0 || sum.SkippedIncremental != 1 || sum.SkippedGate != 1 {
		t.Errorf("resume should skip everything, got %+v", sum)
	}
}

func TestRunBackfillCanceledNotRecorded(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	writeSession(t, projects, "proj", "abrt", 6)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // user abort before processing
	judge := fakeJudge{err: context.Canceled}
	sum, err := RunBackfill(ctx, noRepo, fixedJudge(judge), Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute})
	if err == nil {
		t.Error("want non-nil error on canceled parent ctx")
	}
	if sum.Errored != 0 {
		t.Errorf("canceled session must not be recorded as errored: %+v", sum)
	}
	m, _ := loadManifest()
	if _, ok := m["abrt"]; ok {
		t.Error("canceled session must leave no manifest entry")
	}
}

func TestRunBackfillReprocessesOnNewerMtime(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	writeSession(t, projects, "proj", "grow", 6)
	judge := fakeJudge{fields: substantialJudged()}
	opts := Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute}
	if _, err := RunBackfill(context.Background(), noRepo, fixedJudge(judge), opts); err != nil {
		t.Fatal(err)
	}
	// bump the transcript mtime beyond the stamped decode-time value
	p := filepath.Join(projects, "proj", "grow.jsonl")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	sum, err := RunBackfill(context.Background(), noRepo, fixedJudge(judge), opts)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Analyzed != 1 || sum.SkippedIncremental != 0 {
		t.Errorf("a newer transcript mtime must re-process, got %+v", sum)
	}
}

func TestRunBackfillRecordsErrored(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	writeSession(t, projects, "proj", "boom", 6)
	judge := fakeJudge{err: errors.New("judge failed")}
	sum, err := RunBackfill(context.Background(), noRepo, fixedJudge(judge), Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute})
	if err != nil {
		t.Fatalf("loop should continue past errors, not return: %v", err)
	}
	if sum.Errored != 1 {
		t.Errorf("want Errored=1, got %+v", sum)
	}
	m, _ := loadManifest()
	if e, ok := m["boom"]; !ok || e.Outcome != "errored" {
		t.Errorf("want errored manifest entry, got %+v ok=%v", e, ok)
	}
}

func TestBackfillSkipStaleGateOnThresholdChange(t *testing.T) {
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	mt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	now := mt.Add(time.Minute)
	ref := transcript.TranscriptRef{SessionID: "s", Mtime: mt}
	m := map[string]ManifestEntry{"s": {SessionID: "s", TranscriptMtime: mt, Outcome: "gated", Threshold: 5}}
	if _, skip := backfillSkip(ref, m, Options{MinAssistantTurns: 5}, now); !skip {
		t.Error("same threshold should skip")
	}
	if _, skip := backfillSkip(ref, m, Options{MinAssistantTurns: 3}, now); skip {
		t.Error("lower threshold should re-evaluate (not skip)")
	}
}

func TestBackfillSkipQuiet(t *testing.T) {
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	ref := transcript.TranscriptRef{SessionID: "s1", Path: "/p/s1.jsonl", Mtime: now.Add(-1 * time.Hour)}
	cases := []struct {
		name     string
		quietFor time.Duration
		mtimeAgo time.Duration
		reason   string
		skip     bool
	}{
		{"disabled", 0, time.Hour, "", false},
		{"inside window", 24 * time.Hour, time.Hour, "quiet", true},
		{"at boundary", 24 * time.Hour, 24 * time.Hour, "", false},
		{"outside window", 24 * time.Hour, 25 * time.Hour, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ref
			r.Mtime = now.Add(-tc.mtimeAgo)
			reason, skip := backfillSkip(r, map[string]ManifestEntry{}, Options{QuietFor: tc.quietFor}, now)
			if reason != tc.reason || skip != tc.skip {
				t.Fatalf("got (%q,%v) want (%q,%v)", reason, skip, tc.reason, tc.skip)
			}
		})
	}
}

// Incremental is checked before quiet, so a session that is both analyzed-fresh and
// inside the quiet window still reports "incremental" — Done/Gated counts keep their
// pre-existing meaning and quiet only ever picks up sessions incremental didn't.
func TestBackfillSkipIncrementalBeatsQuiet(t *testing.T) {
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	mt := now.Add(-1 * time.Hour) // inside a 24h quiet window
	if err := WriteAnalysis(AgentSessionAnalysis{Stats: AgentSessionStats{SessionID: "s1"}, TranscriptMtime: mt}); err != nil {
		t.Fatal(err)
	}
	ref := transcript.TranscriptRef{SessionID: "s1", Path: "/p/s1.jsonl", Mtime: mt}
	reason, skip := backfillSkip(ref, map[string]ManifestEntry{}, Options{QuietFor: 24 * time.Hour}, now)
	if reason != "incremental" || !skip {
		t.Fatalf("got (%q,%v) want (\"incremental\",true)", reason, skip)
	}
}

func TestPlanCountsQuiet(t *testing.T) {
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	refs := []transcript.TranscriptRef{
		{SessionID: "quiet1", Path: "/h/.claude/projects/-Users-r-Developer-tmux-ctrl/quiet1.jsonl", Mtime: now.Add(-1 * time.Hour)},
	}
	c := planCounts(refs, map[string]ManifestEntry{}, Options{QuietFor: 24 * time.Hour, MinAssistantTurns: 5}, now)
	if c.Quiet != 1 || c.ToProcess != 0 {
		t.Errorf("want Quiet=1 ToProcess=0, got %+v", c)
	}
}

func TestMetaTranscriptExclusion(t *testing.T) {
	meta := []string{
		"/h/.claude/projects/-Users-r-Developer-tmux-ctrl--worktrees-insights-generation/aa.jsonl",
		"/h/.claude/projects/-Users-r-Developer-tmux-ctrl--worktrees-insights-generation-src/bb.jsonl",
		"/h/.claude/projects/-Users-r-Developer-insights-eval-data/cc.jsonl",
		"/h/.claude/projects/-Users-r-Developer-alpha--worktrees-run-insights-command/dd.jsonl",
		"/h/.claude/projects/-Users-r-Developer-alpha--worktrees-facet-extractor/ee.jsonl",
		"/h/.claude/projects/-private-tmp-claude-501--Users-r--worktrees-insights-generation-x-scratchpad/ff.jsonl",
	}
	for _, p := range meta {
		if !metaTranscript(p) {
			t.Errorf("want meta-excluded: %s", p)
		}
	}
	nonMeta := []string{
		"/h/.claude/projects/-Users-r-Developer-tmux-ctrl/gg.jsonl",
		"/h/.claude/projects/-Users-r-Developer-alpha--worktrees-fix-login/hh.jsonl",
		"/h/.claude/projects/-Users-r-Developer-dotfiles/ii.jsonl",
	}
	for _, p := range nonMeta {
		if metaTranscript(p) {
			t.Errorf("want kept: %s", p)
		}
	}
}

func TestPlanCountsMetaEvenUnderForce(t *testing.T) {
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	refs := []transcript.TranscriptRef{
		{SessionID: "meta1", Path: "/h/.claude/projects/-Users-r-Developer-insights-eval-data/meta1.jsonl"},
		{SessionID: "real1", Path: "/h/.claude/projects/-Users-r-Developer-tmux-ctrl/real1.jsonl"},
	}
	c := planCounts(refs, map[string]ManifestEntry{}, Options{Force: true, MinAssistantTurns: 5}, time.Now())
	if c.Meta != 1 || c.ToProcess != 1 {
		t.Errorf("want Meta=1 ToProcess=1 under --force, got %+v", c)
	}
}
