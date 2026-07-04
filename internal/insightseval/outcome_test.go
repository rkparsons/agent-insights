package insightseval

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"tmux-ctrl/internal/synthesis"
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
	_, err := RunOutcome(context.Background(), opts)
	if err == nil {
		t.Fatal("3 consecutive LLM failures must park the run")
	}
}

type failingSynth struct{}

func (failingSynth) Synthesize(context.Context, synthesis.EvidenceBundle) (synthesis.RawSynthesis, error) {
	return synthesis.RawSynthesis{}, context.DeadlineExceeded
}
