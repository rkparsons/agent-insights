package insightseval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tmux-ctrl/internal/synthesis"
)

// buildScoreFixture mirrors buildOutcomeFixture with the bucket named
// tmux-ctrl so the embedded rubrics with repos [tmux-ctrl, ...] see items,
// and with statuses seeded (scoring fail-closes on missing statuses).
func buildScoreFixture(t *testing.T) (string, OutcomeOptions) {
	t.Helper()
	withFakeCredentials(t)
	data, _ := buildCorpusFixture(t)
	pool := filepath.Join(data, "baseline-pool", "v1")
	writePoolAnalysis(t, pool, "s1", "/Users/x/Developer/tmux-ctrl", 3)
	writePoolAnalysis(t, pool, "s2", "/Users/x/Developer/tmux-ctrl", 4)
	b := Benchmark{
		FrozenAt: time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
		Buckets: map[string]BucketPopulations{
			"tmux-ctrl": {RepoPath: "/Users/x/Developer/tmux-ctrl",
				AsConsumed: []string{"s1", "s2"}, Scoring: []string{"s1", "s2"}, Resolved: true},
		},
		Statuses: map[string]string{},
	}
	if err := writeJSON(filepath.Join(data, "benchmark.json"), b); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "global", "CLAUDE.md"), "frozen")
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "global", "settings.json"), "{}")
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "repos", "tmux-ctrl", "CLAUDE.md"), "repo rules")
	if _, err := SeedStatuses(data); err != nil {
		t.Fatal(err)
	}
	skillL2, skillL1 := t.TempDir(), t.TempDir()
	mustWriteFile(t, filepath.Join(skillL2, "SKILL.md"), "l2 skill v1")
	mustWriteFile(t, filepath.Join(skillL1, "SKILL.md"), "l1 skill v1")
	return data, OutcomeOptions{
		DataDir: data, CacheDir: t.TempDir(), ClaudeVersion: "1.0.0 (test)",
		SkillDirs: map[string]string{
			"analyzing-agent-sessions":       skillL1,
			"synthesizing-workflow-insights": skillL2,
		},
	}
}

// scriptedMatcher: probes always behave (near-miss stays unmatched); target
// payloads answer from the per-rubric script; everything else is absent.
type scriptedMatcher struct {
	responses map[string]MatchResult
	calls     int
}

func (s *scriptedMatcher) Match(_ context.Context, p MatchPayload) (MatchResult, error) {
	s.calls++
	if len(p.Items) == 1 && strings.HasPrefix(p.Items[0].ID, "probe/") {
		switch p.Items[0].ID {
		case "probe/recall", "probe/negative_recall":
			return MatchResult{Matches: []ItemMatch{{ItemID: p.Items[0].ID, Granularity: "full",
				NuanceResults: trues(len(p.Rubric.RequiredNuances)), ForbiddenFormsMatched: []int{}}}}, nil
		default: // near_miss: the forbidden form must stay unmatched
			return MatchResult{}, nil
		}
	}
	if r, ok := s.responses[p.Rubric.ID]; ok {
		return r, nil
	}
	return MatchResult{}, nil
}

func runScoreFixture(t *testing.T) (OutcomeOptions, RunRecord) {
	t.Helper()
	_, opts := buildScoreFixture(t)
	fs := &fakeSynth{raw: synthesis.RawSynthesis{
		Themes: []synthesis.RawTheme{{Title: "T", Kind: "friction", Summary: "s", EvidenceIDs: []string{"F1"}}},
	}}
	opts.Synth = fs
	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return opts, rec
}

func m1Match() MatchResult {
	// M1 has exactly 2 required nuances (rubrics/M1.yaml)
	return MatchResult{Matches: []ItemMatch{{ItemID: "tmux-ctrl/theme/0", Granularity: "full",
		NuanceResults: []bool{true, true}, ForbiddenFormsMatched: []int{}}}}
}

func TestScoreRunEndToEndFreshBaseline(t *testing.T) {
	opts, _ := runScoreFixture(t)
	sm := &scriptedMatcher{responses: map[string]MatchResult{"M1": m1Match()}}
	v, arts, err := ScoreRun(context.Background(), ScoreOptions{
		DataDir: opts.DataDir, CacheDir: opts.CacheDir, ClaudeVersion: "1.0.0 (test)",
		Matcher: sm, ScoredAt: time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if !v.HardFail { // every tmux-ctrl HIGH must_pass target is absent here
		t.Fatal("all-miss fixture must hard-fail")
	}
	if v.PartB["M1"] != "full" {
		t.Fatalf("gap progress: %+v", v.PartB)
	}
	var m1 TargetVerdict
	for _, tv := range v.Targets {
		if tv.ID == "M1" {
			m1 = tv
		}
	}
	found := false
	for _, tr := range m1.Triggers {
		if tr.Type == "ratchet_candidate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("M1 would-pass must card a ratchet candidate: %+v", m1.Triggers)
	}
	if v.Delta == nil || !v.Delta.FreshBaseline {
		t.Fatalf("delta: %+v", v.Delta)
	}
	if v.CardCount == 0 || arts.CardsDir == "" {
		t.Fatalf("cards: %+v", arts)
	}
	if _, err := os.Stat(filepath.Join(arts.CardsDir, "cards.md")); err != nil {
		t.Fatal(err)
	}
	if arts.RunsPath == "" {
		t.Fatal("clean verdict must commit to runs/")
	}
	if _, err := os.Stat(arts.RunsPath); err != nil {
		t.Fatal(err)
	}
}

func TestScoreRunSecondRunCachedWithBaseline(t *testing.T) {
	opts, _ := runScoreFixture(t)
	sm := &scriptedMatcher{responses: map[string]MatchResult{"M1": m1Match()}}
	base := ScoreOptions{DataDir: opts.DataDir, CacheDir: opts.CacheDir,
		ClaudeVersion: "1.0.0 (test)", Matcher: sm}
	base.ScoredAt = time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)
	v1, _, err := ScoreRun(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	callsAfterFirst := sm.calls
	base.ScoredAt = base.ScoredAt.Add(time.Hour)
	v2, _, err := ScoreRun(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if sm.calls != callsAfterFirst {
		t.Fatalf("second score must be fully matcher-cached: %d → %d", callsAfterFirst, sm.calls)
	}
	if v2.Delta == nil || v2.Delta.FreshBaseline {
		t.Fatalf("second run must find the committed baseline: %+v", v2.Delta)
	}
	if v2.Delta.BaselineRun != v1.ScoredAt.Format("2006-01-02T15-04-05Z")+".json" {
		t.Fatalf("baseline name: %q", v2.Delta.BaselineRun)
	}
	if len(v2.Delta.Flips) != 0 {
		t.Fatalf("identical cached outcomes must not flip: %+v", v2.Delta.Flips)
	}
}

func TestScoreRunRejectsL1SampleAndEmptyRecords(t *testing.T) {
	cacheDir := t.TempDir()
	rec := RunRecord{RanAt: time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC), Scope: "l2",
		Population: "scoring", L1Sample: &L1SampleResult{}}
	if err := writeJSON(filepath.Join(cacheDir, "run-records", "r.json"), rec); err != nil {
		t.Fatal(err)
	}
	_, _, err := ScoreRun(context.Background(), ScoreOptions{DataDir: t.TempDir(), CacheDir: cacheDir,
		ClaudeVersion: "1.0.0 (test)", Matcher: &scriptedMatcher{},
		ScoredAt: time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)})
	if err == nil || !strings.Contains(err.Error(), "insights eval outcome") {
		t.Fatalf("l1-sample/empty records must fail closed, never score vacuously: %v", err)
	}
}

func TestScoreRunAbortsOnProbeFailure(t *testing.T) {
	opts, _ := runScoreFixture(t)
	pm := &probeMatcher{generous: true} // matches the near-miss form as full
	_, _, err := ScoreRun(context.Background(), ScoreOptions{
		DataDir: opts.DataDir, CacheDir: opts.CacheDir, ClaudeVersion: "1.0.0 (test)",
		Matcher: pm, ScoredAt: time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)})
	if err == nil || !strings.Contains(err.Error(), "probe") {
		t.Fatalf("generosity drift must invalidate scoring: %v", err)
	}
}

func TestFindCardByPrefix(t *testing.T) {
	cacheDir := t.TempDir()
	k := AdjKey{TargetID: "C-01", Statement: "s", IDSetHash: idSetHash([]string{"a"}), RubricHash: "h", Trigger: "flip"}
	cards := []Card{
		{KeyHash: k.Hash(), TargetID: "C-01", Trigger: "flip", Adjudicable: true, Key: k},
		{KeyHash: cacheKey("card", "C-02", "sample_split"), TargetID: "C-02", Trigger: "sample_split", Adjudicable: false},
	}
	if _, err := WriteCards(cacheDir, "ts1", cards); err != nil {
		t.Fatal(err)
	}
	got, err := FindCardByPrefix(cacheDir, k.Hash()[:12])
	if err != nil || got.TargetID != "C-01" {
		t.Fatalf("find: %+v %v", got, err)
	}
	if _, err := FindCardByPrefix(cacheDir, "ffff"); err == nil {
		t.Fatal("unknown prefix must error")
	}
	if _, err := FindCardByPrefix(cacheDir, cards[1].KeyHash[:12]); err == nil || !strings.Contains(err.Error(), "not adjudicable") {
		t.Fatalf("informational card must refuse adjudication: %v", err)
	}
}
