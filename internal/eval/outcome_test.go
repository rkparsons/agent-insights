package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/skills"
)

// buildOutcomeFixture: corpus fixture (s1,s2) + pool + benchmark + minimal
// config-snapshot, returning (dataDir, opts) ready for RunOutcome.
func buildOutcomeFixture(t *testing.T) (string, OutcomeOptions) {
	t.Helper()
	withFakeCredentials(t)
	data, plain := buildCorpusFixture(t)
	_ = plain
	pool := filepath.Join(data, "baseline-pool", "v1")
	writePoolAnalysis(t, pool, "s1", "/Users/dev/Developer/myrepo", 3)
	writePoolAnalysis(t, pool, "s2", "/Users/dev/Developer/myrepo", 4)
	b := Benchmark{
		FrozenAt: time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
		Buckets: map[string]BucketPopulations{
			"myrepo": {RepoPath: "/Users/dev/Developer/myrepo",
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

// Task 8-10: this asserted the v1 L2 stage (per-repo synthesizer + Finalize),
// which plan Task 7 removed from the pipeline; the global-carding replacement (raw/verify cache keys, per-sample records) lands
// in plan Task 8.
func TestRunOutcomeL2CachesAndRecords(t *testing.T) {
	t.Skip("L2 eval stage rebuilt in plan Tasks 8-10")
}

// Task 8-10: this asserted the v1 L2 stage (per-repo synthesizer + Finalize),
// which plan Task 7 removed from the pipeline; the skill-hash re-keying it pins moves to the v2 raw key (plus the asset-corpus
// hash) in plan Task 10.
func TestRunOutcomeSkillEditReKeysL2Only(t *testing.T) {
	t.Skip("L2 eval stage rebuilt in plan Tasks 8-10")
}

// TestRunOutcomePopulationRejectsUnknown keeps the fail-closed population
// check. Task 8-10: the consecutive-LLM-failure park it also covered belongs
// to the L2 stage and is re-asserted with the global run in plan Task 8.
func TestRunOutcomePopulationAndFailurePolicy(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	opts.Population = "nonsense"
	if _, err := RunOutcome(context.Background(), opts); err == nil {
		t.Fatal("unknown population must error")
	}
}

// TestRunOutcomeSweepsStaleScratchDirs: an interrupted run's scratch dir holds
// a materialized credential, so the sweep must happen on every run — including
// one that fails at the L2 stage.
func TestRunOutcomeSweepsStaleScratchDirs(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	stale := filepath.Join(opts.CacheDir, "scratch", "stale", "config", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte(`{"leaked":"credential"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := RunOutcome(context.Background(), opts); err == nil {
		t.Fatal("Task 8-10: the L2 stage must fail closed until the v2 rework")
	}
	scratchRoot := filepath.Join(opts.CacheDir, "scratch")
	if _, err := os.Stat(scratchRoot); !os.IsNotExist(err) {
		t.Fatalf("scratch root must not survive a run (stale dir swept, own dir deferred-removed): stat err = %v", err)
	}
}

// Task 8-10: this asserted the v1 L2 stage (per-repo synthesizer + Finalize),
// which plan Task 7 removed from the pipeline; per-sample LLM failure counting is re-derived with the global run in plan Task 8.
func TestRunOutcomeConsecutiveFailureReset(t *testing.T) {
	t.Skip("L2 eval stage rebuilt in plan Tasks 8-10")
}
