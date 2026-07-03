package insightseval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tmux-ctrl/internal/insights"
	"tmux-ctrl/internal/synthesis"
)

// buildFixtureWorld fabricates a projects tree, an insights pool, and a live
// synthesis dir wired through the env overrides, and returns the data dir.
func buildFixtureWorld(t *testing.T) string {
	t.Helper()
	projects, insightsDir, data := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", insightsDir)
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
	rep, err := RunFreeze(data)
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
	if _, err := RunFreeze(data); err != nil {
		t.Fatalf("re-run: %v", err)
	}
}

func TestRunFreezeSkewSkipsPool(t *testing.T) {
	data := buildFixtureWorld(t)
	// grow the transcript after analysis: mtime now differs from the stamp
	proj := filepath.Join(os.Getenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR"), "-Users-x-Developer-myrepo")
	f, err := os.OpenFile(filepath.Join(proj, "s1.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n{\"type\":\"user\"}"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	rep, err := RunFreeze(data)
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
