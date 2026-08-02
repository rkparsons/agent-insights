package synthesis

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tmux-ctrl/internal/insights"
)

func writeAnalysisFixture(t *testing.T, adir, id, repo string) {
	t.Helper()
	var a insights.AgentSessionAnalysis
	a.Stats.SessionID = id
	a.Stats.Repo = repo
	a.Stats.Cwd = repo
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adir, id+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeSynthesisFixture writes a synthesis/<repoKey>/<date>.json for repoKey
// (the RepoKey basename, e.g. "fresh" from a repo path ".../fresh").
func writeSynthesisFixture(t *testing.T, dir, repoKey string, generatedAt time.Time, analyzedCount int) {
	t.Helper()
	sdir := filepath.Join(dir, "synthesis", repoKey)
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := RepoSynthesis{Repo: repoKey, GeneratedAt: generatedAt, Window: Window{AnalyzedCount: analyzedCount}}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	name := generatedAt.Format("2006-01-02") + ".json"
	if err := os.WriteFile(filepath.Join(sdir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunSynthesizeDueFilter(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", dir)
	adir := filepath.Join(dir, "analyses")
	if err := os.MkdirAll(adir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnalysisFixture(t, adir, "s1", "/Users/dev/Developer/stale")
	writeAnalysisFixture(t, adir, "s2", "/Users/dev/Developer/fresh")
	writeSynthesisFixture(t, dir, "fresh", time.Now().UTC(), 1)

	sum, err := RunSynthesize(context.Background(), nil, Options{DryRun: true, Due: true, MinSessions: 1})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Repos != 1 {
		t.Fatalf("due filter: got %d repos want 1 (stale only)", sum.Repos)
	}
}

func TestRunSynthesizeDueFilterNoneDue(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", dir)
	adir := filepath.Join(dir, "analyses")
	if err := os.MkdirAll(adir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnalysisFixture(t, adir, "s1", "/Users/dev/Developer/stale")
	writeAnalysisFixture(t, adir, "s2", "/Users/dev/Developer/fresh")
	writeSynthesisFixture(t, dir, "stale", time.Now().UTC(), 1)
	writeSynthesisFixture(t, dir, "fresh", time.Now().UTC(), 1)

	sum, err := RunSynthesize(context.Background(), nil, Options{DryRun: true, Due: true, MinSessions: 1})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Repos != 0 {
		t.Fatalf("no-due case: got %d repos want 0", sum.Repos)
	}
}

func TestRunSynthesizeDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", dir)
	// seed 12 client-project analyses so it clears the floor
	adir := filepath.Join(dir, "analyses")
	os.MkdirAll(adir, 0o755)
	for i := 0; i < 12; i++ {
		writeAnalysisFixture(t, adir, "s"+string(rune('a'+i)), "/Users/dev/Developer/client-project")
	}
	sum, err := RunSynthesize(context.Background(), fakeSynth{}, Options{DryRun: true, MinSessions: 10})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Repos != 1 || sum.Written != 0 {
		t.Errorf("dry-run summary = %+v, want 1 repo / 0 written", sum)
	}
	if _, err := os.Stat(filepath.Join(dir, "synthesis")); !os.IsNotExist(err) {
		t.Error("dry-run must not write the synthesis dir")
	}
}

// TestRunSynthesizeBlocksOnPrivacyLeak covers the write path guarded by scanReport:
// an LLM-authored field (theme Title) that reaches Render unfiltered must trip the
// privacy scan and block Store, not just get logged.
func TestRunSynthesizeBlocksOnPrivacyLeak(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", dir)
	adir := filepath.Join(dir, "analyses")
	os.MkdirAll(adir, 0o755)
	for i := 0; i < 10; i++ {
		writeAnalysisFixture(t, adir, "s"+string(rune('a'+i)), "/Users/dev/Developer/client-project")
	}
	fake := fakeSynth{raw: RawSynthesis{
		Themes: []RawTheme{{Title: "Leaked path /Users/dev/secret/notes", Kind: "friction",
			Summary: "some friction theme summary"}},
	}}
	sum, err := RunSynthesize(context.Background(), fake, Options{MinSessions: 10})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Skipped != 1 || sum.Written != 0 {
		t.Errorf("summary = %+v, want Skipped=1 / Written=0 (privacy-blocked)", sum)
	}
	if _, err := os.Stat(filepath.Join(dir, "synthesis", "client-project")); !os.IsNotExist(err) {
		t.Error("privacy-blocked repo must not produce a synthesis/<repo> output dir")
	}
}
