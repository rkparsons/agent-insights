package eval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
	"github.com/rkparsons/agent-insights/skills"
)

// buildOutcomeFixture: corpus fixture (s1,s2) + pool + benchmark + minimal
// config-snapshot, returning (dataDir, opts) ready for RunOutcome. The two
// sessions sit in DIFFERENT buckets so every run exercises the cross-repo
// shape the v2 synthesis is built around.
func buildOutcomeFixture(t *testing.T) (string, OutcomeOptions) {
	t.Helper()
	withFakeCredentials(t)
	data, plain := buildCorpusFixture(t)
	_ = plain
	pool := filepath.Join(data, "baseline-pool", "v1")
	writePoolAnalysis(t, pool, "s1", "/Users/dev/Developer/alpha", 3)
	writePoolAnalysis(t, pool, "s2", "/Users/dev/Developer/beta", 4)
	b := Benchmark{
		FrozenAt: time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
		Buckets: map[string]BucketPopulations{
			"alpha": {RepoPath: "/Users/dev/Developer/alpha",
				AsConsumed: []string{"s1"}, Scoring: []string{"s1"}, Resolved: true},
			"beta": {RepoPath: "/Users/dev/Developer/beta",
				AsConsumed: []string{"s2"}, Scoring: []string{"s2"}, Resolved: true},
		},
		Statuses: map[string]string{},
	}
	if err := writeJSON(filepath.Join(data, "benchmark.json"), b); err != nil {
		t.Fatal(err)
	}
	writeMinimalRubricSet(t, data)
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "global", "CLAUDE.md"), "frozen")
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "global", "settings.json"), "{}")
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "repos", "alpha", "CLAUDE.md"), "repo rules")
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "repos", "beta", "CLAUDE.md"), "repo rules")
	skill := t.TempDir()
	mustWriteFile(t, filepath.Join(skill, "SKILL.md"), "l2 skill v1")
	skillL1 := t.TempDir()
	mustWriteFile(t, filepath.Join(skillL1, "SKILL.md"), "l1 skill v1")
	return data, OutcomeOptions{
		DataDir:       data,
		CacheDir:      t.TempDir(),
		ClaudeVersion: "1.0.0 (test)",
		Synth:         &fakeGlobalSynth{raw: mergedRaw()},
		SkillDirs: map[string]string{
			"analyzing-agent-sessions": skillL1,
			skills.SynthesisSkill:      skill,
		},
	}
}

// fakeGlobalSynth scripts the one cross-repo call: the same raw output every
// sample unless errs says that call fails, recording the bundles it was handed.
type fakeGlobalSynth struct {
	raw   insights.RawGlobalSynthesis
	errs  []bool // per call; missing → success
	calls int
	seen  []map[string]synthesis.EvidenceBundle
}

func (f *fakeGlobalSynth) SynthesizeGlobal(_ context.Context, bundles map[string]synthesis.EvidenceBundle) (insights.RawGlobalSynthesis, error) {
	i := f.calls
	f.calls++
	f.seen = append(f.seen, bundles)
	if i < len(f.errs) && f.errs[i] {
		return insights.RawGlobalSynthesis{}, errors.New("scripted L2 failure")
	}
	return f.raw, nil
}

// mergedRaw is a verifier-valid v2 synthesis whose one finding merges alpha's
// and beta's evidence, plus one dropped entry. asset.type habit grounds on
// F/S evidence and needs no target/content/audience (verify2.go's table), so
// the fixture stays valid without inventing files.
func mergedRaw() insights.RawGlobalSynthesis {
	return insights.RawGlobalSynthesis{
		SchemaVersion: 3,
		Findings: []insights.RawFinding{{
			Rank: 1, Title: "State the goal before editing",
			Statement:     "open every session by writing the goal down",
			RankRationale: "both repos show sessions that land faster with a stated goal",
			Asset:         insights.AssetJSON{Type: "habit"},
			EvidenceIDs:   []string{"alpha/S1", "beta/S1"},
			AlreadyAdopted: insights.AdoptedJSON{
				Verdict: "no",
			},
		}},
		Dropped: []insights.DroppedJSON{{
			Summary:     "single mention of a comment-style nit",
			Reason:      "one session only, no recurrence",
			EvidenceIDs: []string{"alpha/S1"},
		}},
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

// The L2 stage is one global sample loop over every bucket's bundle: the
// synthesizer sees all repos at once, samples are recorded on the record (not
// per bucket), and a re-run serves every sample from the cache.
func TestRunOutcomeGlobalSamplesCacheAndRecord(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	fs := opts.Synth.(*fakeGlobalSynth)

	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Buckets) != 2 {
		t.Fatalf("buckets: %+v", rec.Buckets)
	}
	if fs.calls != 3 {
		t.Fatalf("one call per sample over ALL bundles, calls = %d", fs.calls)
	}
	if len(fs.seen[0]) != 2 || fs.seen[0]["alpha"].Repo != "alpha" || fs.seen[0]["beta"].Repo != "beta" {
		t.Fatalf("synthesizer must see every bucket's bundle at once: %+v", fs.seen[0])
	}
	if len(rec.SampleOutputs) != 3 {
		t.Fatalf("global samples: %+v", rec.SampleOutputs)
	}
	seen := map[string]bool{}
	for _, s := range rec.SampleOutputs {
		if !s.Fresh || s.RawKey == "" || s.VerifiedKey == "" {
			t.Fatalf("sample %d: %+v", s.SampleIndex, s)
		}
		if seen[s.RawKey] {
			t.Fatalf("sample %d reuses another sample's raw key", s.SampleIndex)
		}
		seen[s.RawKey] = true
	}

	// the verified output is the v2 snapshot, carrying the merged finding
	cache := NewCache(opts.CacheDir)
	var vo VerifiedOutput
	hit, err := cache.Get("verify", rec.SampleOutputs[0].VerifiedKey, &vo)
	if err != nil || !hit {
		t.Fatalf("verified output: hit=%v err=%v", hit, err)
	}
	if len(vo.Snapshot.Findings) != 1 || len(vo.Snapshot.Dropped) != 1 {
		t.Fatalf("snapshot: %+v", vo.Snapshot)
	}
	if got := vo.Snapshot.Findings[0].Repos; len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("merged finding repos: %v", got)
	}
	if vo.Snapshot.Findings[0].SessionCount != 2 {
		t.Fatalf("merged finding session count: %+v", vo.Snapshot.Findings[0])
	}

	// second run: every stage cached, no LLM spend
	rec2, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fs.calls != 3 {
		t.Fatalf("re-run must be fully cached, calls = %d", fs.calls)
	}
	if rec2.CacheMisses != 0 {
		t.Fatalf("re-run misses = %d (warnings %v)", rec2.CacheMisses, rec2.Warnings)
	}
	for i, s := range rec2.SampleOutputs {
		if s.Fresh || s.RawKey != rec.SampleOutputs[i].RawKey || s.VerifiedKey != rec.SampleOutputs[i].VerifiedKey {
			t.Fatalf("cached sample %d must reuse the same keys: %+v vs %+v", i, s, rec.SampleOutputs[i])
		}
	}
}

// A skill edit re-keys the L2 stage only: the frozen corpus and its bundles are
// untouched, so nothing upstream is re-bought.
func TestRunOutcomeSkillEditReKeysL2Only(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	fs := opts.Synth.(*fakeGlobalSynth)
	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	mustWriteFile(t, filepath.Join(opts.SkillDirs[skills.SynthesisSkill], "SKILL.md"), "l2 skill v2")
	rec2, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fs.calls != 6 {
		t.Fatalf("edited skill must re-buy all 3 samples, calls = %d", fs.calls)
	}
	if rec2.Buckets[0].BundleKey != rec.Buckets[0].BundleKey {
		t.Fatal("a skill edit must not re-key the bundle stage")
	}
	if rec2.SampleOutputs[0].RawKey == rec.SampleOutputs[0].RawKey {
		t.Fatal("a skill edit must re-key the raw L2 entry")
	}
}

// TestRunOutcomePopulationRejectsUnknown keeps the fail-closed population check.
func TestRunOutcomePopulationAndFailurePolicy(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	opts.Population = "nonsense"
	if _, err := RunOutcome(context.Background(), opts); err == nil {
		t.Fatal("unknown population must error")
	}
}

// TestRunOutcomeSweepsStaleScratchDirs: an interrupted run's scratch dir holds
// a materialized credential, so the sweep must happen on every run.
func TestRunOutcomeSweepsStaleScratchDirs(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	stale := filepath.Join(opts.CacheDir, "scratch", "stale", "config", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte(`{"leaked":"credential"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := RunOutcome(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	scratchRoot := filepath.Join(opts.CacheDir, "scratch")
	if _, err := os.Stat(scratchRoot); !os.IsNotExist(err) {
		t.Fatalf("scratch root must not survive a run (stale dir swept, own dir deferred-removed): stat err = %v", err)
	}
}

// Consecutive L2 failures park the run; a success in between resets the count,
// and the samples that did land are still recorded.
func TestRunOutcomeConsecutiveFailureReset(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	opts.Samples = 4
	opts.Synth = &fakeGlobalSynth{raw: mergedRaw(), errs: []bool{true, true, false, true}}
	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatalf("two failures then a success must not park: %v", err)
	}
	if len(rec.SampleOutputs) != 1 || rec.SampleOutputs[0].SampleIndex != 2 {
		t.Fatalf("only the successful sample is recorded: %+v", rec.SampleOutputs)
	}
	if len(rec.Warnings) < 3 {
		t.Fatalf("every failed sample must warn: %v", rec.Warnings)
	}

	_, opts2 := buildOutcomeFixture(t)
	opts2.Synth = &fakeGlobalSynth{raw: mergedRaw(), errs: []bool{true, true, true}}
	if _, err := RunOutcome(context.Background(), opts2); err == nil {
		t.Fatal("three consecutive L2 failures must park the run")
	}
}

// A synthesis the verifier rejects yields no sample at all — a run whose every
// sample is rejected must not look scoreable.
func TestRunOutcomeRejectsUnverifiableSynthesis(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	bad := mergedRaw()
	bad.Findings[0].EvidenceIDs = []string{"alpha/F99"} // dangling citation
	opts.Synth = &fakeGlobalSynth{raw: bad}
	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.SampleOutputs) != 0 {
		t.Fatalf("unverifiable samples must not be recorded: %+v", rec.SampleOutputs)
	}
	if len(rec.Warnings) == 0 {
		t.Fatal("a rejected synthesis must be warned about")
	}
	if _, _, err := loadScoreableRecord(ScoreOptions{RecordPath: rec.RecordPath}); err == nil {
		t.Fatal("a record with zero samples must fail closed at scoring")
	}
}

// The v2 model READS the frozen asset corpus (global CLAUDE.md/skills/settings
// and every repo's CLAUDE.md), so a cached raw L2 result is only valid for the
// corpus that produced it: an unchanged corpus must serve the same key, and a
// changed corpus file — repo-side as well as global-side — must re-key. This is
// the one cache-key mistake that costs real money silently (a stale L2 answer
// served across a corpus re-freeze reads as a free run), so both directions are
// asserted.
func TestRunOutcomeRawKeyTracksAssetCorpus(t *testing.T) {
	data, opts := buildOutcomeFixture(t)
	fs := opts.Synth.(*fakeGlobalSynth)

	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	// unchanged corpus: same keys, no spend
	same, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fs.calls != 3 {
		t.Fatalf("unchanged corpus must not re-buy any sample, calls = %d", fs.calls)
	}
	for i, s := range same.SampleOutputs {
		if s.Fresh || s.RawKey != rec.SampleOutputs[i].RawKey {
			t.Fatalf("unchanged corpus re-keyed sample %d: %+v vs %+v", i, s, rec.SampleOutputs[i])
		}
	}

	// a repo CLAUDE.md the model can read changes: every sample must re-key
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "repos", "alpha", "CLAUDE.md"), "repo rules v2")
	afterRepo, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fs.calls != 6 {
		t.Fatalf("a changed repo CLAUDE.md must re-buy all 3 samples, calls = %d", fs.calls)
	}
	for i, s := range afterRepo.SampleOutputs {
		if !s.Fresh || s.RawKey == rec.SampleOutputs[i].RawKey {
			t.Fatalf("sample %d served a stale raw entry across a repo-corpus change: %+v", i, s)
		}
	}

	// the global half of the corpus re-keys too
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "global", "CLAUDE.md"), "frozen v2")
	afterGlobal, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range afterGlobal.SampleOutputs {
		if !s.Fresh || s.RawKey == afterRepo.SampleOutputs[i].RawKey {
			t.Fatalf("sample %d served a stale raw entry across a global-corpus change: %+v", i, s)
		}
	}
}

// The env pin overlays EVERY live skill into the config dir the manifest
// advertises as readable, so any skill edit — not just the synthesis skill's —
// must re-key the raw L2 entry.
func TestRunOutcomeRawKeyCoversEveryOverlaidSkill(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	fs := opts.Synth.(*fakeGlobalSynth)
	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	mustWriteFile(t, filepath.Join(opts.SkillDirs["analyzing-agent-sessions"], "SKILL.md"), "l1 skill v2")
	edited, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fs.calls != 6 {
		t.Fatalf("an edit to a readable non-synthesis skill must re-buy all 3 samples, calls = %d", fs.calls)
	}
	for i, s := range edited.SampleOutputs {
		if !s.Fresh || s.RawKey == rec.SampleOutputs[i].RawKey {
			t.Fatalf("sample %d served a stale raw entry across a skill edit: %+v", i, s)
		}
	}
	// and an untouched skill set keys identically
	again, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range again.SampleOutputs {
		if s.Fresh || s.RawKey != edited.SampleOutputs[i].RawKey {
			t.Fatalf("unchanged skills re-keyed sample %d", i)
		}
	}
}

// Which repo roots the manifest can name is itself an input: a resolvable root
// and an "unavailable" one are different prompts over identical corpus bytes.
func TestAssetRootsHashTracksResolvedRoots(t *testing.T) {
	both := assetRootsHash(insights.Config{Repos: []string{"/data/config-snapshot/repos/alpha", "/data/config-snapshot/repos/beta"}})
	if both != assetRootsHash(insights.Config{Repos: []string{"/moved/config-snapshot/repos/alpha", "/moved/config-snapshot/repos/beta"}}) {
		t.Fatal("relocating the data repo must not re-key an unchanged corpus")
	}
	if both == assetRootsHash(insights.Config{Repos: []string{"/data/config-snapshot/repos/alpha"}}) {
		t.Fatal("a root the manifest can no longer name must re-key")
	}
}

// The configured synthesis_model is the run's L2 identity: it reaches the
// record, the verified snapshot's meta, and the raw cache key — a model switch
// is a re-baseline event, never a cache hit.
func TestRunOutcomeConfiguredSynthesisModelKeysTheRun(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Models["l2"] != insights.DefaultSynthesisModel {
		t.Fatalf("unset model must default: %v", rec.Models)
	}

	opts.SynthesisModel = "claude-opus-5"
	switched, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if switched.Models["l2"] != "claude-opus-5" {
		t.Fatalf("record must name the configured model: %v", switched.Models)
	}
	for i, s := range switched.SampleOutputs {
		if !s.Fresh || s.RawKey == rec.SampleOutputs[i].RawKey {
			t.Fatalf("sample %d served a stale raw entry across a model switch: %+v", i, s)
		}
	}
	var vo VerifiedOutput
	hit, err := NewCache(opts.CacheDir).Get("verify", switched.SampleOutputs[0].VerifiedKey, &vo)
	if err != nil || !hit {
		t.Fatalf("verified output: hit=%v err=%v", hit, err)
	}
	if vo.Snapshot.Meta.Model != "claude-opus-5" {
		t.Fatalf("the run's config must carry the configured model into verification: %q", vo.Snapshot.Meta.Model)
	}
}

// The synthesis manifest must name the FROZEN repo corpus, never a live
// checkout: eval redirects repo roots into config-snapshot/repos, and a bucket
// with no frozen config degrades to "unavailable" visibly rather than silently.
func TestFrozenAssetConfigRedirectsRepoRoots(t *testing.T) {
	data, _ := buildOutcomeFixture(t)
	cfg, warnings := frozenAssetConfig(data, []string{"alpha", "beta"}, "claude-fable-5")
	want := []string{
		filepath.Join(data, "config-snapshot", "repos", "alpha"),
		filepath.Join(data, "config-snapshot", "repos", "beta"),
	}
	if len(cfg.Repos) != 2 || cfg.Repos[0] != want[0] || cfg.Repos[1] != want[1] {
		t.Fatalf("repo roots = %v, want %v", cfg.Repos, want)
	}
	if cfg.DotfilesRepo != "" {
		t.Fatalf("eval config must omit dotfiles_repo (degraded escalation is the reproducibility answer): %q", cfg.DotfilesRepo)
	}
	if cfg.SynthesisModel != "claude-fable-5" {
		t.Fatalf("model = %q", cfg.SynthesisModel)
	}
	if len(warnings) != 0 {
		t.Fatalf("fully frozen corpus must not warn: %v", warnings)
	}

	if err := os.RemoveAll(filepath.Join(data, "config-snapshot", "repos", "beta")); err != nil {
		t.Fatal(err)
	}
	cfg, warnings = frozenAssetConfig(data, []string{"alpha", "beta"}, "claude-fable-5")
	if len(cfg.Repos) != 1 || cfg.Repos[0] != want[0] {
		t.Fatalf("an unfrozen bucket must be omitted, not faked: %v", cfg.Repos)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "beta") {
		t.Fatalf("an unfrozen bucket must warn: %v", warnings)
	}
}
