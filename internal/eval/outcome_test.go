package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/synthesis"
	"github.com/rkparsons/agent-insights/skills"
)

type fakeSynth struct {
	raw   synthesis.RawSynthesis
	calls int
}

func (f *fakeSynth) Synthesize(ctx context.Context, b synthesis.EvidenceBundle) (synthesis.RawSynthesis, error) {
	f.calls++
	return f.raw, nil
}

// buildOutcomeFixture: corpus fixture (s1,s2) + pool + benchmark + minimal
// config-snapshot, returning (dataDir, opts) ready for RunOutcome.
func buildOutcomeFixture(t *testing.T) (string, OutcomeOptions) {
	t.Helper()
	withFakeCredentials(t)
	data, plain := buildCorpusFixture(t)
	_ = plain
	pool := filepath.Join(data, "baseline-pool", "v1")
	writePoolAnalysis(t, pool, "s1", "/Users/x/Developer/myrepo", 3)
	writePoolAnalysis(t, pool, "s2", "/Users/x/Developer/myrepo", 4)
	b := Benchmark{
		FrozenAt: time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
		Buckets: map[string]BucketPopulations{
			"myrepo": {RepoPath: "/Users/x/Developer/myrepo",
				AsConsumed: []string{"s1", "s2"}, Scoring: []string{"s1", "s2"}, Resolved: true},
		},
		Statuses: map[string]string{},
	}
	if err := writeJSON(filepath.Join(data, "benchmark.json"), b); err != nil {
		t.Fatal(err)
	}
	writeMinimalRubricSet(t, data)
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "global", "CLAUDE.md"), "frozen")
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "global", "settings.json"), "{}")
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "repos", "myrepo", "CLAUDE.md"), "repo rules")
	skill := t.TempDir()
	mustWriteFile(t, filepath.Join(skill, "SKILL.md"), "l2 skill v1")
	skillL1 := t.TempDir()
	mustWriteFile(t, filepath.Join(skillL1, "SKILL.md"), "l1 skill v1")
	return data, OutcomeOptions{
		DataDir:       data,
		CacheDir:      t.TempDir(),
		ClaudeVersion: "1.0.0 (test)",
		SkillDirs: map[string]string{
			"analyzing-agent-sessions":       skillL1,
			"synthesizing-workflow-insights": skill,
		},
	}
}

// The env pin's skill hashes feed the l1/l2 cache keys, so the dirs it hashes
// must be the embedded skills the binary ships and the hashing must agree with
// the skills package's own — a mismatch orphans every paid cache entry.
func TestDefaultSkillDirsAreTheEmbeddedSkills(t *testing.T) {
	dirs, cleanup, err := defaultSkillDirs()
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != len(skills.Names()) {
		t.Fatalf("dirs = %v, want one per embedded skill %v", dirs, skills.Names())
	}
	var root string
	for _, name := range skills.Names() {
		dir, ok := dirs[name]
		if !ok {
			t.Fatalf("%s missing from %v", name, dirs)
		}
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			t.Fatalf("%s not materialized: %v", name, err)
		}
		root = filepath.Dir(dir)
	}
	got, err := hashTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != skills.TreeHash() {
		t.Fatalf("hashTree(materialized) = %s, skills.TreeHash() = %s", got, skills.TreeHash())
	}
	cleanup()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("cleanup left %s behind (err=%v)", root, err)
	}
}

func TestRunOutcomeL2CachesAndRecords(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	fs := &fakeSynth{raw: synthesis.RawSynthesis{
		Themes: []synthesis.RawTheme{{Title: "T", Kind: "friction", Summary: "s", EvidenceIDs: []string{"F1"}}},
	}}
	opts.Synth = fs

	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fs.calls != 3 { // default k=3 samples, distinct cache keys
		t.Fatalf("synth calls = %d", fs.calls)
	}
	if len(rec.Buckets) != 1 || len(rec.Buckets[0].Samples) != 3 {
		t.Fatalf("buckets: %+v", rec.Buckets)
	}
	for _, s := range rec.Buckets[0].Samples {
		if !s.Fresh {
			t.Fatal("first run must be fresh")
		}
	}
	for _, field := range []string{rec.ManifestHash, rec.BenchmarkHash, rec.RubricSetHash,
		rec.EnvHash, rec.ConfigSnapshotHash, rec.Models["l2"], rec.SchemaHashes["l2"],
		rec.CodeVersions["facts"], rec.CodeVersions["synthesis"]} {
		if field == "" {
			t.Fatalf("reproducibility record incomplete: %+v", rec)
		}
	}
	// verified output is retrievable and byte-stable (frozen GeneratedAt)
	cache := NewCache(opts.CacheDir)
	var vo VerifiedOutput
	hit, err := cache.Get("verify", rec.Buckets[0].Samples[0].VerifiedKey, &vo)
	if err != nil || !hit {
		t.Fatalf("verified output missing: hit=%v err=%v", hit, err)
	}
	if !vo.Synthesis.GeneratedAt.Equal(time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("GeneratedAt not frozen: %v", vo.Synthesis.GeneratedAt)
	}
	if len(vo.Raw.Themes) != 1 || vo.Synthesis.Repo != "myrepo" {
		t.Fatalf("verified output: %+v", vo)
	}

	rec2, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fs.calls != 3 {
		t.Fatalf("second run must be fully cached, calls = %d", fs.calls)
	}
	for i, s := range rec2.Buckets[0].Samples {
		if s.Fresh {
			t.Fatal("cached samples must not be fresh")
		}
		if s.RawKey != rec.Buckets[0].Samples[i].RawKey {
			t.Fatal("keys must be stable")
		}
	}
}

func TestRunOutcomeSkillEditReKeysL2Only(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	fs := &fakeSynth{}
	opts.Synth = fs
	rec1, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(opts.SkillDirs["synthesizing-workflow-insights"], "SKILL.md"), "l2 skill v2")
	rec2, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fs.calls != 6 {
		t.Fatalf("skill edit must re-run all samples: calls = %d", fs.calls)
	}
	if rec2.Buckets[0].BundleHash != rec1.Buckets[0].BundleHash {
		t.Fatal("bundle stage must be untouched by a skill edit")
	}
	if rec2.Buckets[0].Samples[0].RawKey == rec1.Buckets[0].Samples[0].RawKey {
		t.Fatal("L2 keys must change with the skill hash")
	}
}

func TestRunOutcomePopulationAndFailurePolicy(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	opts.Population = "nonsense"
	if _, err := RunOutcome(context.Background(), opts); err == nil {
		t.Fatal("unknown population must error")
	}
	opts.Population = ""
	opts.Synth = failingSynth{}
	rec, err := RunOutcome(context.Background(), opts)
	if err == nil {
		t.Fatal("3 consecutive LLM failures must park the run")
	}
	// parked run must preserve the in-flight bucket with partial state
	if len(rec.Buckets) != 1 {
		t.Fatalf("parked bucket missing: got %d buckets", len(rec.Buckets))
	}
	if rec.Buckets[0].Bucket != "myrepo" {
		t.Fatalf("bucket name mismatch: got %q", rec.Buckets[0].Bucket)
	}
	if rec.Buckets[0].PoolSliceHash == "" {
		t.Fatal("partial bucket missing PoolSliceHash")
	}
}

type failingSynth struct{}

func (failingSynth) Synthesize(context.Context, synthesis.EvidenceBundle) (synthesis.RawSynthesis, error) {
	return synthesis.RawSynthesis{}, context.DeadlineExceeded
}

type flakySynth struct {
	calls int
}

func (f *flakySynth) Synthesize(ctx context.Context, b synthesis.EvidenceBundle) (synthesis.RawSynthesis, error) {
	f.calls++
	if f.calls <= 2 {
		return synthesis.RawSynthesis{}, context.DeadlineExceeded
	}
	return synthesis.RawSynthesis{
		Themes: []synthesis.RawTheme{{Title: "T", Kind: "friction", Summary: "s", EvidenceIDs: []string{"F1"}}},
	}, nil
}

func TestRunOutcomeSweepsStaleScratchDirs(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	stale := filepath.Join(opts.CacheDir, "scratch", "stale", "config", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte(`{"leaked":"credential"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := &fakeSynth{raw: synthesis.RawSynthesis{
		Themes: []synthesis.RawTheme{{Title: "T", Kind: "friction", Summary: "s", EvidenceIDs: []string{"F1"}}},
	}}
	opts.Synth = fs

	if _, err := RunOutcome(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	scratchRoot := filepath.Join(opts.CacheDir, "scratch")
	if _, err := os.Stat(scratchRoot); !os.IsNotExist(err) {
		t.Fatalf("scratch root must not survive a completed run (stale dir swept, own dir deferred-removed): stat err = %v", err)
	}
}

func TestRunOutcomeConsecutiveFailureReset(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	fs := &flakySynth{}
	opts.Synth = fs
	// With 3 samples default, each sample is one call:
	// call 1 (sample 0) fails, warn
	// call 2 (sample 1) fails, warn
	// call 3 (sample 2) succeeds → record has 1 sample, no park
	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatalf("flaky synth should recover after 2 failures: %v", err)
	}
	if len(rec.Buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(rec.Buckets))
	}
	if len(rec.Buckets[0].Samples) != 1 {
		t.Fatalf("expected 1 sample after recovery, got %d", len(rec.Buckets[0].Samples))
	}
	// Verify warnings mention "L2 failed" for the two failures
	l2FailedCount := 0
	for _, w := range rec.Warnings {
		if strings.Contains(w, "L2 failed") {
			l2FailedCount++
		}
	}
	if l2FailedCount < 2 {
		t.Fatalf("expected at least 2 warnings with 'L2 failed', got %d (warnings: %v)", l2FailedCount, rec.Warnings)
	}
}
