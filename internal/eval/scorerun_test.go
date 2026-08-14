package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
)

// alternatingSynth emits output the verifier rejects on its first call and
// valid output afterwards: a run that loses a sample to the VERIFIER while
// other samples land.
type alternatingSynth struct {
	good, bad insights.RawGlobalSynthesis
	calls     int
}

func (a *alternatingSynth) SynthesizeGlobal(_ context.Context, _ map[string]synthesis.EvidenceBundle) (insights.RawGlobalSynthesis, error) {
	a.calls++
	if a.calls == 1 {
		return a.bad, nil
	}
	return a.good, nil
}

// buildScoreFixture is buildOutcomeFixture with statuses seeded (scoring
// fail-closes on missing statuses).
func buildScoreFixture(t *testing.T) (string, OutcomeOptions) {
	t.Helper()
	data, opts := buildOutcomeFixture(t)
	if _, err := SeedStatuses(data); err != nil {
		t.Fatal(err)
	}
	return data, opts
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

// runScoreFixture produces a scoreable run record for the score-path tests:
// one global synthesis per sample over the alpha+beta bundles.
func runScoreFixture(t *testing.T) (OutcomeOptions, RunRecord) {
	t.Helper()
	_, opts := buildScoreFixture(t)
	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return opts, rec
}

func m1Match() MatchResult {
	// M1 has exactly 2 required nuances (writeMinimalRubricSet's M1.yaml); the
	// fixture's one finding merges alpha and beta, and is scored once.
	return MatchResult{Matches: []ItemMatch{{ItemID: "finding/1", Granularity: "full",
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
	if !v.HardFail { // every HIGH must_pass target is absent from the fixture synthesis
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

// Samples lost at synthesis time (LLM failure or a verifier rejection) shrink
// the scoring denominator silently — the median is taken over survivors and
// sample agreement is trivially 1.0. Until the pre-spend refusal channel is
// re-sourced (Task 9), scoring must at least say so in the verdict.
func TestScoreRunWarnsOnLostSamples(t *testing.T) {
	_, opts := buildScoreFixture(t)
	opts.Synth = &fakeGlobalSynth{raw: mergedRaw(), errs: []bool{true, true}} // only sample 2 lands
	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.SampleOutputs) != 1 || rec.Samples != 3 {
		t.Fatalf("fixture must lose 2 of 3 samples: %+v", rec.SampleOutputs)
	}
	v, _, err := ScoreRun(context.Background(), ScoreOptions{
		DataDir: opts.DataDir, CacheDir: opts.CacheDir, ClaudeVersion: "1.0.0 (test)",
		Matcher:  &scriptedMatcher{responses: map[string]MatchResult{"M1": m1Match()}},
		ScoredAt: time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range v.Warnings {
		if strings.Contains(w, "1 of 3") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the verdict must name the lost samples: %v", v.Warnings)
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
	// RubricSetHash runs before the L1-sample rejection check, so the data dir
	// still needs a valid rubrics/ — this test targets record-shape rejection,
	// not rubric loading.
	dataDir := t.TempDir()
	writeMinimalRubricSet(t, dataDir)
	_, _, err := ScoreRun(context.Background(), ScoreOptions{DataDir: dataDir, CacheDir: cacheDir,
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

func TestScoreRunRejectsInvalidStatusValues(t *testing.T) {
	opts, _ := runScoreFixture(t)
	b, ok, err := loadBenchmark(opts.DataDir)
	if err != nil || !ok {
		t.Fatal(err)
	}
	b.Statuses["C-07"] = "must-pass" // typo'd manual ratchet edit
	if err := writeJSON(filepath.Join(opts.DataDir, "benchmark.json"), b); err != nil {
		t.Fatal(err)
	}
	_, _, err = ScoreRun(context.Background(), ScoreOptions{
		DataDir: opts.DataDir, CacheDir: opts.CacheDir, ClaudeVersion: "1.0.0 (test)",
		Matcher: &scriptedMatcher{}, ScoredAt: time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)})
	if err == nil || !strings.Contains(err.Error(), "invalid status") {
		t.Fatalf("a typo'd status value must fail closed, never silently un-score a target: %v", err)
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

// The pre-spend refusal gate: a sample whose raw output the verifier REJECTED
// is a broken pipeline, not a thin run — scoring refuses before it buys a
// single matcher read, however many other samples landed.
func TestScoreRefusesRecordWithSynthesisHardErrors(t *testing.T) {
	_, opts := buildScoreFixture(t)
	rejected := mergedRaw()
	rejected.Findings[0].EvidenceIDs = []string{"alpha/F99"} // dangling citation
	opts.Synth = &alternatingSynth{good: mergedRaw(), bad: rejected}
	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.SampleOutputs) == 0 || len(rec.VerifierRejections) == 0 {
		t.Fatalf("fixture must land some samples and reject others: %d landed, rejections %v",
			len(rec.SampleOutputs), rec.VerifierRejections)
	}
	sm := &scriptedMatcher{responses: map[string]MatchResult{"M1": m1Match()}}
	_, _, err = ScoreRun(context.Background(), ScoreOptions{
		DataDir: opts.DataDir, CacheDir: opts.CacheDir, ClaudeVersion: "1.0.0 (test)",
		Matcher: sm, ScoredAt: time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)})
	if err == nil || !strings.Contains(err.Error(), "hard errors") {
		t.Fatalf("a rejected sample must refuse scoring: %v", err)
	}
	if sm.calls != 0 {
		t.Fatalf("refusal must precede every matcher read, calls = %d", sm.calls)
	}
	if _, statErr := os.Stat(filepath.Join(opts.DataDir, "runs")); !os.IsNotExist(statErr) {
		t.Fatal("a refused record must not commit a verdict")
	}
}

// The gate's side of the dropped-citation bargain: a drop that is the only
// thing suppressing a recall floor reaches the human as a recognition card,
// through the same contested-card surface every other trigger uses.
func TestScoreRunCardsDroppedEvidenceThatSuppressesARecallFloor(t *testing.T) {
	data, opts := buildScoreFixture(t)
	// alpha's session now carries friction, so the alpha bundle has an F* item
	// and the friction recall floor applies to it.
	a := insights.AgentSessionAnalysis{
		Stats: insights.AgentSessionStats{SessionID: "s1", Repo: "/Users/dev/Developer/alpha", AssistantTurns: 3},
		JudgedFields: insights.JudgedFields{
			UnderlyingGoal: "goal-s1", Outcome: "fully_achieved", SessionType: "single_task",
			FrictionIncidents: []insights.FrictionIncident{{Type: "tooling", OneLine: "re-ran the build by hand"}},
		},
		TranscriptMtime: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := writeJSON(filepath.Join(data, "baseline-pool", "v1", "s1.json"), a); err != nil {
		t.Fatal(err)
	}
	raw := mergedRaw()
	raw.Dropped[0].EvidenceIDs = []string{"alpha/F1"} // the only mention of alpha's friction
	opts.Synth = &fakeGlobalSynth{raw: raw}
	if _, err := RunOutcome(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	v, arts, err := ScoreRun(context.Background(), ScoreOptions{
		DataDir: opts.DataDir, CacheDir: opts.CacheDir, ClaudeVersion: "1.0.0 (test)",
		Matcher:  &scriptedMatcher{responses: map[string]MatchResult{"M1": m1Match()}},
		ScoredAt: time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Tier1.FrictionRecallMisses) != 0 || v.Tier1.DroppedSuppressions == 0 {
		t.Fatalf("the drop must suppress the friction floor, not clear it: %+v", v.Tier1)
	}
	md, err := os.ReadFile(filepath.Join(arts.CardsDir, "cards.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), droppedSuppression) || !strings.Contains(string(md), "comment-style nit") {
		t.Fatalf("the dropped entry must reach the adjudication set:\n%s", md)
	}
}

// The fabrication gate end to end, against the REAL verifier: a quote the
// model invented is dropped by VerifyGlobal into meta.validation_notes, and the
// tier-1 rate reads it back. Couples the note format to its only consumer — a
// reworded note would otherwise silently zero the fabrication signal.
func TestScoreRunFabricationRateReadsVerifierNotes(t *testing.T) {
	_, opts := buildScoreFixture(t)
	raw := mergedRaw()
	raw.Findings[0].Quotes = []string{"the user never said this"} // no such quote in the pool
	opts.Synth = &fakeGlobalSynth{raw: raw}
	if _, err := RunOutcome(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	v, _, err := ScoreRun(context.Background(), ScoreOptions{
		DataDir: opts.DataDir, CacheDir: opts.CacheDir, ClaudeVersion: "1.0.0 (test)",
		Matcher:  &scriptedMatcher{responses: map[string]MatchResult{"M1": m1Match()}},
		ScoredAt: time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if v.Tier1.MaxRawFabricationRate != 1 {
		t.Fatalf("the one cited quote was fabricated: rate = %v", v.Tier1.MaxRawFabricationRate)
	}
	found := false
	for _, r := range v.HardFailReasons {
		if strings.Contains(r, "fabrication rate") {
			found = true
		}
	}
	if !found {
		t.Fatalf("fabrication over the gate must hard-fail: %v", v.HardFailReasons)
	}
}

func TestScoreTargetsDevLoopNeverCommits(t *testing.T) {
	opts, _ := runScoreFixture(t)
	sm := &scriptedMatcher{responses: map[string]MatchResult{"M1": m1Match()}}
	results, _, err := ScoreTargets(context.Background(), ScoreOptions{
		DataDir: opts.DataDir, CacheDir: opts.CacheDir, ClaudeVersion: "1.0.0 (test)",
		Matcher: sm, Targets: []string{"M1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Rubric.ID != "M1" {
		t.Fatalf("only requested targets score: %+v", results)
	}
	if results[0].Verdict.Granularity != "full" || len(results[0].Samples) != 3 {
		t.Fatalf("M1 verdict: %+v", results[0].Verdict)
	}
	if _, err := os.Stat(filepath.Join(opts.DataDir, "runs")); !os.IsNotExist(err) {
		t.Fatalf("dev loop must never write runs/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(opts.CacheDir, "verdicts")); !os.IsNotExist(err) {
		t.Fatalf("dev loop must never persist a verdict: %v", err)
	}
	if _, err := os.Stat(filepath.Join(opts.CacheDir, "cards")); !os.IsNotExist(err) {
		t.Fatalf("dev loop must never write cards: %v", err)
	}
}

func TestScoreTargetsSampleLimitAndValidation(t *testing.T) {
	opts, rec := runScoreFixture(t)
	sm := &scriptedMatcher{responses: map[string]MatchResult{"M1": m1Match()}}
	base := ScoreOptions{DataDir: opts.DataDir, CacheDir: opts.CacheDir,
		ClaudeVersion: "1.0.0 (test)", Matcher: sm}

	limited := base
	limited.Targets, limited.MaxSamples = []string{"M1"}, 1
	results, _, err := ScoreTargets(context.Background(), limited)
	if err != nil {
		t.Fatal(err)
	}
	if len(results[0].Samples) != 1 {
		t.Fatalf("--samples 1 must score one sample: %+v", results[0].Samples)
	}

	// Empty cache + explicit record: a typo'd id must be named BEFORE any
	// session loading — not drowned in a bundle-missing error, and never
	// after probe spend.
	unknown := base
	unknown.Targets = []string{"M1", "C-99"}
	unknown.CacheDir = t.TempDir()
	unknown.RecordPath = rec.RecordPath
	unknown.Matcher = &scriptedMatcher{}
	if _, _, err := ScoreTargets(context.Background(), unknown); err == nil || !strings.Contains(err.Error(), "C-99") {
		t.Fatalf("unknown target id must fail closed pre-session: %v", err)
	}
	if calls := unknown.Matcher.(*scriptedMatcher).calls; calls != 0 {
		t.Fatalf("a typo'd target id must error before any matcher spend (probes included), calls = %d", calls)
	}

	negative := base
	negative.Targets = []string{"N-01"}
	negative.CacheDir = t.TempDir()
	negative.RecordPath = rec.RecordPath
	negative.Matcher = &scriptedMatcher{}
	if _, _, err := ScoreTargets(context.Background(), negative); err == nil || !strings.Contains(err.Error(), "N-01") {
		t.Fatalf("negative rubrics have no per-target dev loop; must error pre-session: %v", err)
	}
	if calls := negative.Matcher.(*scriptedMatcher).calls; calls != 0 {
		t.Fatalf("negative-id rejection must also fire pre-spend, calls = %d", calls)
	}
}

func TestScoreRunDefaultsNonPositiveRepeats(t *testing.T) {
	opts, _ := runScoreFixture(t)
	sm := &scriptedMatcher{responses: map[string]MatchResult{"M1": m1Match()}}
	v, _, err := ScoreRun(context.Background(), ScoreOptions{
		DataDir: opts.DataDir, CacheDir: opts.CacheDir, ClaudeVersion: "1.0.0 (test)",
		Matcher: sm, Repeats: -1,
		ScoredAt: time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("negative repeats must default to 3, not error or no-op: %v", err)
	}
	if len(v.Probes) != 3 {
		t.Fatalf("probes must have run with the defaulted repeats: %+v", v.Probes)
	}
	for _, p := range v.Probes {
		if len(p.Granularities) != 3 {
			t.Fatalf("probe %s must carry 3 defaulted repeats, got %v", p.Class, p.Granularities)
		}
	}
}
