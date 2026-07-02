package synthesis

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
