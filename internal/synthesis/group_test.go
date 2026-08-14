package synthesis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

func analysisWith(repo, cwd string) insights.AgentSessionAnalysis {
	var a insights.AgentSessionAnalysis
	a.Stats.Repo = repo
	a.Stats.Cwd = cwd
	return a
}

func TestRepoKey(t *testing.T) {
	home := "/Users/dev"
	cfg := insights.Config{Aliases: map[string]string{"oldname": "tmux-ctrl"}}
	cases := []struct {
		name, repo, cwd, want string
	}{
		{"configured repo", home + "/Developer/alpha", home + "/Developer/alpha", "alpha"},
		{"configured worktree", home + "/Developer/alpha/.worktrees/feat-1", home + "/Developer/alpha/.worktrees/feat-1", "alpha"},
		{"oldname worktree folds to tmux-ctrl", "", home + "/Developer/oldname/.worktrees/preview/src", "tmux-ctrl"},
		{"oldname plain folds to tmux-ctrl", "", home + "/Developer/oldname", "tmux-ctrl"},
		{"unconfigured developer repo", "", home + "/Developer/somelib/src", "somelib"},
		{"non-developer dotfiles dropped", "", home + "/.dotfiles", ""},
		{"home path dropped", "", home, ""},
		{"trash dropped", "", home + "/.Trash/x/src", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RepoKey(analysisWith(c.repo, c.cwd), cfg)
			if got != c.want {
				t.Errorf("RepoKey(repo=%q,cwd=%q) = %q, want %q", c.repo, c.cwd, got, c.want)
			}
		})
	}
}

// TestLoadAnalysesStampsAnalyzedAt pins the store-file mtime as the "when was
// this analyzed" instant: the stored artifact carries no such timestamp, and
// due-ness reads it to decide what is new since the last global snapshot.
func TestLoadAnalysesStampsAnalyzedAt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_INSIGHTS_DIR", dir)
	adir := filepath.Join(dir, "analyses")
	if err := os.MkdirAll(adir, 0o755); err != nil {
		t.Fatal(err)
	}
	var a insights.AgentSessionAnalysis
	a.Stats.SessionID = "s1"
	a.TranscriptMtime = time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(adir, "s1.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	written := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}

	got, err := LoadAnalyses()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("loaded %d analyses, want 1", len(got))
	}
	if !got[0].AnalyzedAt.Equal(written) {
		t.Errorf("AnalyzedAt = %v, want the store file's mtime %v", got[0].AnalyzedAt, written)
	}
	if !got[0].TranscriptMtime.Equal(a.TranscriptMtime) {
		t.Errorf("TranscriptMtime = %v, want the stored %v (the transcript's own mtime is untouched)", got[0].TranscriptMtime, a.TranscriptMtime)
	}
}

func TestGroupByRepoFloor(t *testing.T) {
	home := "/Users/dev"
	var as []insights.AgentSessionAnalysis
	for i := 0; i < 12; i++ {
		as = append(as, analysisWith(home+"/Developer/alpha", home+"/Developer/alpha"))
	}
	for i := 0; i < 3; i++ { // oldname folds to tmux-ctrl but is below floor on its own...
		as = append(as, analysisWith("", home+"/Developer/oldname/.worktrees/w/src"))
	}
	for i := 0; i < 8; i++ { // ...plus configured tmux-ctrl → combined 11 clears floor
		as = append(as, analysisWith(home+"/Developer/tmux-ctrl", home+"/Developer/tmux-ctrl"))
	}
	as = append(as, analysisWith("", home+"/.dotfiles")) // dropped by RepoKey

	groups := GroupByRepo(as, 10, insights.Config{Aliases: map[string]string{"oldname": "tmux-ctrl"}})
	if len(groups["alpha"]) != 12 {
		t.Errorf("alpha = %d, want 12", len(groups["alpha"]))
	}
	if len(groups["tmux-ctrl"]) != 11 {
		t.Errorf("tmux-ctrl = %d, want 11 (8 configured + 3 oldname fold)", len(groups["tmux-ctrl"]))
	}
	if _, ok := groups[""]; ok {
		t.Error("empty key must never appear as a group")
	}
	if _, ok := groups["src"]; ok {
		t.Error("basename(cwd) junk repo 'src' must never appear")
	}
}
