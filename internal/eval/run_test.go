package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
)

// buildFixtureWorld fabricates a projects tree, an insights pool, and a live
// synthesis dir wired through the env overrides, and returns the data dir.
func buildFixtureWorld(t *testing.T) string {
	t.Helper()
	projects, insightsDir, data := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("AGENT_INSIGHTS_PROJECTS_DIR", projects)
	t.Setenv("AGENT_INSIGHTS_DIR", insightsDir)
	t.Setenv("HOME", t.TempDir())

	proj := filepath.Join(projects, "-Users-x-Developer-myrepo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(proj, "s1.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"user"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	if err := os.Chtimes(transcript, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	a := insights.AgentSessionAnalysis{
		Stats: insights.AgentSessionStats{
			SessionID: "s1", Repo: "/Users/x/Developer/myrepo",
			Start: time.Date(2026, 6, 25, 9, 0, 0, 0, time.UTC),
		},
		TranscriptMtime: mtime,
	}
	if err := insights.WriteAnalysis(a); err != nil {
		t.Fatal(err)
	}

	truth := synthesis.RepoSynthesis{
		Repo:        "myrepo",
		GeneratedAt: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		Window:      synthesis.Window{From: "2026-06-25", To: "2026-06-25", AnalyzedCount: 1},
	}
	raw, err := json.Marshal(truth)
	if err != nil {
		t.Fatal(err)
	}
	truthDir := filepath.Join(insightsDir, "synthesis", "myrepo")
	if err := os.MkdirAll(truthDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(truthDir, "2026-07-02.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRunFreezeEndToEnd(t *testing.T) {
	data := buildFixtureWorld(t)
	rep, err := RunFreeze(data, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Issues.Clean() {
		t.Fatalf("issues: %+v", rep.Issues)
	}
	if rep.PoolSkipped {
		t.Fatal("clean freeze must copy the baseline pool")
	}
	for _, p := range []string{
		filepath.Join(data, "corpus", "s1.jsonl.gz"),
		filepath.Join(data, "manifest.json"),
		filepath.Join(data, "benchmark.json"),
		filepath.Join(data, "ground-truth", "myrepo", "2026-07-02.json"),
		filepath.Join(data, "baseline-pool", "v1", "s1.json"),
		filepath.Join(data, "README.md"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing %s", p)
		}
	}
	// idempotent re-run
	if _, err := RunFreeze(data, insights.Config{}); err != nil {
		t.Fatalf("re-run: %v", err)
	}
}

func TestRunFreezeSkewSkipsPool(t *testing.T) {
	data := buildFixtureWorld(t)
	// grow the transcript after analysis: mtime now differs from the stamp
	proj := filepath.Join(os.Getenv("AGENT_INSIGHTS_PROJECTS_DIR"), "-Users-x-Developer-myrepo")
	f, err := os.OpenFile(filepath.Join(proj, "s1.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n{\"type\":\"user\"}"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	rep, err := RunFreeze(data, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Issues.Skews) != 1 || !rep.PoolSkipped {
		t.Fatalf("want 1 skew + pool skipped, got %+v skipped=%v", rep.Issues, rep.PoolSkipped)
	}
	if _, err := os.Stat(filepath.Join(data, "baseline-pool")); !os.IsNotExist(err) {
		t.Fatal("baseline-pool must not exist after a skewed freeze")
	}
}

// TestRunFreezeGapRecordedNotBlocking covers a ground-truth-referenced
// session whose raw transcript was pruned before the freeze ever ran (so it
// can never gain a manifest entry): the gap must be recorded, not block the
// pool.
func TestRunFreezeGapRecordedNotBlocking(t *testing.T) {
	data := buildFixtureWorld(t)

	// second session: in the pool (and in the ground-truth window) but its
	// live transcript was pruned before this freeze -- no manifest entry is
	// ever possible for it.
	aPruned := insights.AgentSessionAnalysis{
		Stats: insights.AgentSessionStats{
			SessionID: "s-pruned", Repo: "/Users/x/Developer/myrepo",
			Start: time.Date(2026, 6, 26, 9, 0, 0, 0, time.UTC),
		},
		TranscriptMtime: time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC),
	}
	if err := insights.WriteAnalysis(aPruned); err != nil {
		t.Fatal(err)
	}
	insightsDir := os.Getenv("AGENT_INSIGHTS_DIR")
	truth := synthesis.RepoSynthesis{
		Repo:        "myrepo",
		GeneratedAt: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		Window:      synthesis.Window{From: "2026-06-25", To: "2026-06-26", AnalyzedCount: 2},
	}
	raw, err := json.Marshal(truth)
	if err != nil {
		t.Fatal(err)
	}
	truthDir := filepath.Join(insightsDir, "synthesis", "myrepo")
	if err := os.WriteFile(filepath.Join(truthDir, "2026-07-02.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := RunFreeze(data, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Issues.Blocking() {
		t.Fatalf("gap-only issues must not block: %+v", rep.Issues)
	}
	if !slices.Contains(rep.Issues.Gaps, "myrepo/s-pruned") {
		t.Fatalf("Issues.Gaps = %v, want myrepo/s-pruned", rep.Issues.Gaps)
	}
	if rep.PoolSkipped {
		t.Fatal("gap-only freeze must still write the baseline pool")
	}
	for _, p := range []string{
		filepath.Join(data, "baseline-pool", "v1", "s1.json"),
		filepath.Join(data, "baseline-pool", "v1", "s-pruned.json"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("pool not written: %s: %v", p, err)
		}
	}

	var onDisk Benchmark
	rawBench, err := os.ReadFile(filepath.Join(data, "benchmark.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rawBench, &onDisk); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(onDisk.Buckets["myrepo"].Gaps, "s-pruned") {
		t.Fatalf("benchmark.json bucket gaps = %v, want [s-pruned] (repo prefix stripped)", onDisk.Buckets["myrepo"].Gaps)
	}
}

// TestRunFreezePreservesEntryAfterLiveTranscriptPrunedNotAGap is the
// end-to-end companion to TestFreezeCorpusRerunPreservesEntryAfterLiveTranscriptPruned
// (finding A): a session frozen once must not turn into a benchmark gap just
// because its live transcript is later pruned.
func TestRunFreezePreservesEntryAfterLiveTranscriptPrunedNotAGap(t *testing.T) {
	data := buildFixtureWorld(t)
	rep1, err := RunFreeze(data, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep1.Issues.Clean() {
		t.Fatalf("first freeze must be clean: %+v", rep1.Issues)
	}

	proj := filepath.Join(os.Getenv("AGENT_INSIGHTS_PROJECTS_DIR"), "-Users-x-Developer-myrepo")
	if err := os.Remove(filepath.Join(proj, "s1.jsonl")); err != nil {
		t.Fatal(err)
	}

	rep2, err := RunFreeze(data, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep2.Issues.Clean() {
		t.Fatalf("pruning the live transcript of an already-frozen session must not introduce a gap: %+v", rep2.Issues)
	}
	found := false
	for _, e := range rep2.Manifest.Entries {
		if e.SessionID == "s1" {
			found = true
		}
	}
	if !found {
		t.Fatal("s1 dropped from the manifest after its live transcript was pruned")
	}
}

// TestRunFreezeReusesCanonicalBenchmarkAndPoolOnNewLiveAnalysis covers
// finding F: once benchmark.json and baseline-pool/v1 are canonical, a NEW
// analysis appearing in the live pool must not perturb either — no rebuild,
// no re-copy, no clobbered buckets.
func TestRunFreezeReusesCanonicalBenchmarkAndPoolOnNewLiveAnalysis(t *testing.T) {
	data := buildFixtureWorld(t)
	rep1, err := RunFreeze(data, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep1.Issues.Clean() || rep1.PoolSkipped {
		t.Fatalf("first freeze must be clean with pool written: %+v skipped=%v", rep1.Issues, rep1.PoolSkipped)
	}
	benchBefore, err := os.ReadFile(filepath.Join(data, "benchmark.json"))
	if err != nil {
		t.Fatal(err)
	}
	v1Before, err := os.ReadDir(filepath.Join(data, "baseline-pool", "v1"))
	if err != nil {
		t.Fatal(err)
	}

	// a brand new session lands in the live pool (not referenced by ground
	// truth's AnalyzedCount, so a rebuild would clobber the resolved bucket).
	newAnalysis := insights.AgentSessionAnalysis{
		Stats: insights.AgentSessionStats{
			SessionID: "s2", Repo: "/Users/x/Developer/myrepo",
			Start: time.Date(2026, 6, 26, 9, 0, 0, 0, time.UTC),
		},
		TranscriptMtime: time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC),
	}
	if err := insights.WriteAnalysis(newAnalysis); err != nil {
		t.Fatal(err)
	}

	rep2, err := RunFreeze(data, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep2.Issues.Clean() {
		t.Fatalf("re-run with a new live-pool analysis must stay clean: %+v", rep2.Issues)
	}
	if rep2.PoolCopied != 0 {
		t.Fatalf("PoolCopied = %d, want 0 (v1 already exists, must not be re-copied)", rep2.PoolCopied)
	}
	if rep2.PoolSkipped {
		t.Fatal("PoolSkipped must stay false when v1 already exists and issues are clean")
	}

	benchAfter, err := os.ReadFile(filepath.Join(data, "benchmark.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(benchBefore) != string(benchAfter) {
		t.Fatal("benchmark.json must be byte-identical after a new live-pool analysis")
	}

	v1After, err := os.ReadDir(filepath.Join(data, "baseline-pool", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	namesBefore, namesAfter := []string{}, []string{}
	for _, e := range v1Before {
		namesBefore = append(namesBefore, e.Name())
	}
	for _, e := range v1After {
		namesAfter = append(namesAfter, e.Name())
	}
	if !slices.Equal(namesBefore, namesAfter) {
		t.Fatalf("baseline-pool/v1 contents changed: %v -> %v", namesBefore, namesAfter)
	}
	if _, err := os.Stat(filepath.Join(data, "baseline-pool", "v1", "s2.json")); !os.IsNotExist(err) {
		t.Fatal("new live-pool analysis must NOT be copied into the canonical v1")
	}
}

// TestRunFreezeIgnoresLivePoolDriftOnceV1Canonical covers finding F: once
// baseline-pool/v1 is canonical, the skew check must read v1's stamped mtime,
// not the (possibly independently mutated) live pool.
func TestRunFreezeIgnoresLivePoolDriftOnceV1Canonical(t *testing.T) {
	data := buildFixtureWorld(t)
	rep1, err := RunFreeze(data, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep1.Issues.Clean() || rep1.PoolSkipped {
		t.Fatalf("first freeze must be clean with pool written: %+v skipped=%v", rep1.Issues, rep1.PoolSkipped)
	}

	// mutate the LIVE pool's stamped mtime without touching the transcript or
	// v1 at all — simulates a stray/bogus re-write of the live analysis.
	mutated := insights.AgentSessionAnalysis{
		Stats: insights.AgentSessionStats{
			SessionID: "s1", Repo: "/Users/x/Developer/myrepo",
			Start: time.Date(2026, 6, 25, 9, 0, 0, 0, time.UTC),
		},
		TranscriptMtime: time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC).Add(3 * time.Hour),
	}
	if err := insights.WriteAnalysis(mutated); err != nil {
		t.Fatal(err)
	}

	rep2, err := RunFreeze(data, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep2.Issues.Clean() {
		t.Fatalf("live pool drift must be ignored once v1 is canonical: %+v", rep2.Issues)
	}
	if rep2.PoolSkipped {
		t.Fatal("pool must not be skipped once already canonical")
	}
}

// TestRunFreezeSkewResolvedByRejudgeThenPoolWritten is the missing
// operational-loop test (finding G): a session frozen with a skew, then
// re-judged (re-analyzed against the now-frozen transcript, stamping the
// frozen mtime), must come up clean on the next freeze and get its pool
// written.
func TestRunFreezeSkewResolvedByRejudgeThenPoolWritten(t *testing.T) {
	data := buildFixtureWorld(t)
	proj := filepath.Join(os.Getenv("AGENT_INSIGHTS_PROJECTS_DIR"), "-Users-x-Developer-myrepo")
	f, err := os.OpenFile(filepath.Join(proj, "s1.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n{\"type\":\"user\"}"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	rep1, err := RunFreeze(data, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep1.Issues.Skews) != 1 || !rep1.PoolSkipped {
		t.Fatalf("want a skew + pool skipped on first freeze, got %+v skipped=%v", rep1.Issues, rep1.PoolSkipped)
	}
	var frozenMtime time.Time
	found := false
	for _, e := range rep1.Manifest.Entries {
		if e.SessionID == "s1" {
			frozenMtime = e.Mtime
			found = true
		}
	}
	if !found {
		t.Fatal("s1 missing from manifest after first freeze")
	}

	// re-judge: `agent-insights analyze s1` against the now-frozen
	// (grown) transcript stamps its current mtime.
	rejudged := insights.AgentSessionAnalysis{
		Stats: insights.AgentSessionStats{
			SessionID: "s1", Repo: "/Users/x/Developer/myrepo",
			Start: time.Date(2026, 6, 25, 9, 0, 0, 0, time.UTC),
		},
		TranscriptMtime: frozenMtime,
	}
	if err := insights.WriteAnalysis(rejudged); err != nil {
		t.Fatal(err)
	}

	rep2, err := RunFreeze(data, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep2.Issues.Clean() {
		t.Fatalf("want clean after re-judge stamps the frozen mtime, got %+v", rep2.Issues)
	}
	if rep2.PoolSkipped {
		t.Fatal("pool must be written once the skew is resolved")
	}
	if _, err := os.Stat(filepath.Join(data, "baseline-pool", "v1", "s1.json")); err != nil {
		t.Fatalf("baseline-pool/v1/s1.json not written: %v", err)
	}
}

// TestRunFreezeDoesNotReuseEmptyBenchmark covers finding H: a zero-bucket
// benchmark.json would be vacuously "resolved" by allBucketsResolved, but
// an empty benchmark should never be reused — ground-truth analyses present
// now must cause a rebuild.
func TestRunFreezeDoesNotReuseEmptyBenchmark(t *testing.T) {
	data := buildFixtureWorld(t)

	// Write an empty benchmark.json manually (simulates a previous buggy
	// freeze that produced zero buckets)
	emptyBench := Benchmark{
		FrozenAt: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		Buckets:  map[string]BucketPopulations{},
		Statuses: map[string]string{},
	}
	raw, err := json.Marshal(emptyBench)
	if err != nil {
		t.Fatal(err)
	}
	benchPath := filepath.Join(data, "benchmark.json")
	if err := os.MkdirAll(filepath.Dir(benchPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(benchPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := RunFreeze(data, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Issues.Clean() {
		t.Fatalf("freeze must be clean: %+v", rep.Issues)
	}
	// The key invariant: even with an existing (vacuously-resolved) empty
	// benchmark, a rebuild must happen when ground-truth is present, and buckets
	// must be populated.
	if len(rep.Benchmark.Buckets) == 0 {
		t.Fatal("benchmark.json must be rebuilt with populated buckets (ground-truth present)")
	}
	if _, ok := rep.Benchmark.Buckets["myrepo"]; !ok {
		t.Fatalf("myrepo bucket missing: %v", rep.Benchmark.Buckets)
	}
	if !rep.Benchmark.Buckets["myrepo"].Resolved {
		t.Fatalf("myrepo bucket must be resolved: %+v", rep.Benchmark.Buckets["myrepo"])
	}
}

func TestRunFreezeGroundTruthCanonicalOnce(t *testing.T) {
	data := buildFixtureWorld(t)
	if _, err := RunFreeze(data, insights.Config{}); err != nil {
		t.Fatal(err)
	}
	// a NEW live synthesis lands after the freeze — it must not join ground-truth/
	insightsDir := os.Getenv("AGENT_INSIGHTS_DIR")
	newer := filepath.Join(insightsDir, "synthesis", "myrepo", "2026-07-09.json")
	if err := os.WriteFile(newer, []byte(`{"repo":"myrepo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := RunFreeze(data, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.GroundTruthRetained {
		t.Fatal("re-run must retain the frozen ground truth")
	}
	if _, err := os.Stat(filepath.Join(data, "ground-truth", "myrepo", "2026-07-09.json")); !os.IsNotExist(err) {
		t.Fatal("newer live synthesis leaked into the frozen ground truth")
	}
	if !rep.PoolRetained {
		t.Fatal("re-run with existing v1 must report the pool as retained")
	}
}
