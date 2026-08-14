package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
)

// tier1Fixture builds a record + cache with bundle/verify entries: 3 global
// samples, the first freshCount fresh. emptyLast makes the last sample's
// synthesis empty (no findings at all).
func tier1Fixture(t *testing.T, freshCount int, emptyLast bool) (RunRecord, *Cache) {
	t.Helper()
	cache := NewCache(t.TempDir())
	bundle := synthesis.EvidenceBundle{Repo: "alpha",
		Friction: []synthesis.FrictionItem{{ID: "F1", OneLine: "line", SessionID: "s1"}}}
	if err := cache.Put("bundle", "bk1", bundle); err != nil {
		t.Fatal(err)
	}
	rec := RunRecord{Scope: "l2", Population: "scoring", PoolVersion: "v1",
		Models: map[string]string{"l1": "ml1", "l2": "ml2"}, EnvHash: "e1",
		ManifestHash: "mh", BenchmarkHash: "bh", ConfigSnapshotHash: "ch",
		CodeVersions: map[string]string{"facts": "f1"}, SkillHashes: map[string]string{},
		Buckets: []BucketOutputs{{Bucket: "alpha", Population: []string{"s1", "s2"},
			BundleKey: "bk1", BundleHash: "bh1"}}}
	for i := 0; i < 3; i++ {
		vo := VerifiedOutput{Snapshot: insights.GlobalSynthesisJSON{SchemaVersion: 2,
			Findings: []insights.FindingJSON{{Rank: 1, Title: "T", Statement: "verify first",
				EvidenceIDs: []string{"alpha/F1"}, Repos: []string{"alpha"}}}}}
		if emptyLast && i == 2 {
			vo = VerifiedOutput{Snapshot: insights.GlobalSynthesisJSON{SchemaVersion: 2}}
		}
		key := fmt.Sprintf("vk%d", i)
		if err := cache.Put("verify", key, vo); err != nil {
			t.Fatal(err)
		}
		rec.SampleOutputs = append(rec.SampleOutputs, SampleOutput{SampleIndex: i,
			Fresh: i < freshCount, VerifiedKey: key})
	}
	return rec, cache
}

// The tier-1 gates themselves live in tier1_test.go; this fixture is the
// verdict-composition side (a record whose gates are quiet).
func composeInputs(t *testing.T, results []TargetResult, prior []namedVerdict) (VerdictInputs, *Cache) {
	t.Helper()
	rec, cache := tier1Fixture(t, 0, false)
	return VerdictInputs{Record: rec, RecordName: "rec.json",
		ScoredAt:      time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC),
		RubricSetHash: "r1", MatcherEnvHash: "e1",
		Results: results, Adj: nil, Prior: prior}, cache
}

func targetResult(id, status string, pass bool, gran string) TargetResult {
	r := scoreRubric()
	r.ID = id
	tv := TargetVerdict{ID: id, Part: "regression", Tier: "HIGH", Status: status, PassAt: "full",
		Granularity: gran, Pass: pass, MeetsExpectation: pass,
		HardFail: status == "must_pass" && !pass}
	return TargetResult{Rubric: r, Verdict: tv,
		Samples: []SampleScore{sample(0, gran), sample(1, gran), sample(2, gran)}}
}

func TestComposeVerdictFreshBaselineAndPartA(t *testing.T) {
	results := []TargetResult{
		targetResult("C-01", "must_pass", true, "full"),
		targetResult("C-02", "must_pass", false, "absent"),
		targetResult("M1", "expected_fail", false, "partial"),
	}
	results[2].Verdict.HardFail = false
	results[2].Verdict.MeetsExpectation = true
	in, cache := composeInputs(t, results, nil)
	v, extra, err := ComposeVerdict(in, cache)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Delta.FreshBaseline {
		t.Fatalf("delta: %+v", v.Delta)
	}
	// fresh baseline: every miss cards once
	if len(extra) != 1 || extra[0].TargetID != "C-02" || extra[0].Trigger != "baseline_miss" {
		t.Fatalf("baseline_miss cards: %+v", extra)
	}
	if v.PartA.Scored != 2 || v.PartA.Passed != 1 || v.PartA.WeightedRecall != 0.5 {
		t.Fatalf("part A: %+v", v.PartA)
	}
	if !v.HardFail { // C-02 is a HIGH must_pass miss
		t.Fatalf("hard fail: %+v", v.HardFailReasons)
	}
	if v.PartB["M1"] != "partial" {
		t.Fatalf("part B: %+v", v.PartB)
	}
	if v.Tuple.Models["matcher"] != MatcherModel || v.Tuple.RubricSetHash != "r1" {
		t.Fatalf("tuple: %+v", v.Tuple)
	}
}

func TestComposeVerdictFlipProvisionalFail(t *testing.T) {
	prior := []namedVerdict{{Name: "base.json", V: Verdict{
		ScoredAt: time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC),
		Tuple: ComparisonTuple{Population: "scoring", Scope: "l2", PoolVersion: "v1",
			Models:  map[string]string{"l1": "ml1", "l2": "ml2", "matcher": MatcherModel},
			EnvHash: "e1", RubricSetHash: "r1"},
		Targets: []TargetVerdict{
			{ID: "C-01", Pass: false, Granularity: "absent", PassAt: "full"},
			{ID: "C-02", Pass: true, Granularity: "full", PassAt: "full"},
		}}}}
	results := []TargetResult{
		targetResult("C-01", "must_pass", true, "full"),    // fail → pass flip
		targetResult("C-02", "must_pass", false, "absent"), // pass → fail flip
	}
	in, cache := composeInputs(t, results, prior)
	v, extra, err := ComposeVerdict(in, cache)
	if err != nil {
		t.Fatal(err)
	}
	var c01 TargetVerdict
	for _, tv := range v.Targets {
		if tv.ID == "C-01" {
			c01 = tv
		}
	}
	// fail→pass provisional-fails until adjudicated (spec) — but never
	// hard-fails: the granularity is passing, so it is not a HIGH miss
	if c01.Pass || !c01.ProvisionalFail || c01.HardFail {
		t.Fatalf("flip provisional: %+v", c01)
	}
	flips := 0
	for _, c := range extra {
		if c.Trigger == "flip" {
			flips++
		}
	}
	if flips != 2 {
		t.Fatalf("flip cards: %+v", extra)
	}
	if v.Delta.BaselineRun != "base.json" || len(v.Delta.Flips) != 2 {
		t.Fatalf("delta: %+v", v.Delta)
	}
}

func TestPersistVerdictPrivacyGate(t *testing.T) {
	data, cacheDir := t.TempDir(), t.TempDir()
	v := Verdict{ScoredAt: time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC),
		RecordName: "rec.json", Provenance: map[string]string{"claude_version": "1.0.0"}}
	runsPath, err := PersistVerdict(data, cacheDir, v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runsPath); err != nil {
		t.Fatalf("runs write: %v", err)
	}
	leaky := v
	leaky.ScoredAt = leaky.ScoredAt.Add(time.Hour)
	leaky.RecordName = "/Users/dev/rec.json"
	if _, err := PersistVerdict(data, cacheDir, leaky); err == nil || !strings.Contains(err.Error(), "privacy") {
		t.Fatalf("leaky verdict must not commit: %v", err)
	}
	name := leaky.ScoredAt.Format("2006-01-02T15-04-05Z") + ".json"
	if _, err := os.Stat(filepath.Join(data, "runs", name)); !os.IsNotExist(err) {
		t.Fatal("leaky verdict landed in runs/")
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "verdicts", name)); err != nil {
		t.Fatal("local debug copy must exist even when the scan refuses")
	}
}
