package synthesis

import (
	"testing"

	"tmux-ctrl/internal/insights"
)

func analysisWith(repo, cwd string) insights.AgentSessionAnalysis {
	var a insights.AgentSessionAnalysis
	a.Stats.Repo = repo
	a.Stats.Cwd = cwd
	return a
}

func TestRepoKey(t *testing.T) {
	home := "/Users/dev"
	aliases := map[string]string{"terminal-app": "tmux-ctrl"}
	cases := []struct {
		name, repo, cwd, want string
	}{
		{"configured client-project", home + "/Developer/client-project", home + "/Developer/client-project", "client-project"},
		{"configured worktree", home + "/Developer/client-project/.worktrees/sc-1", home + "/Developer/client-project/.worktrees/sc-1", "client-project"},
		{"terminal-app worktree folds to tmux-ctrl", "", home + "/Developer/terminal-app/.worktrees/preview/src", "tmux-ctrl"},
		{"terminal-app plain folds to tmux-ctrl", "", home + "/Developer/terminal-app", "tmux-ctrl"},
		{"unconfigured developer repo", "", home + "/Developer/somelib/src", "somelib"},
		{"non-developer dotfiles dropped", "", home + "/.dotfiles", ""},
		{"home path dropped", "", home, ""},
		{"trash dropped", "", home + "/.Trash/x/src", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RepoKey(analysisWith(c.repo, c.cwd), aliases)
			if got != c.want {
				t.Errorf("RepoKey(repo=%q,cwd=%q) = %q, want %q", c.repo, c.cwd, got, c.want)
			}
		})
	}
}

func TestGroupByRepoFloor(t *testing.T) {
	home := "/Users/dev"
	var as []insights.AgentSessionAnalysis
	for i := 0; i < 12; i++ {
		as = append(as, analysisWith(home+"/Developer/client-project", home+"/Developer/client-project"))
	}
	for i := 0; i < 3; i++ { // terminal-app folds to tmux-ctrl but is below floor on its own...
		as = append(as, analysisWith("", home+"/Developer/terminal-app/.worktrees/w/src"))
	}
	for i := 0; i < 8; i++ { // ...plus configured tmux-ctrl → combined 11 clears floor
		as = append(as, analysisWith(home+"/Developer/tmux-ctrl", home+"/Developer/tmux-ctrl"))
	}
	as = append(as, analysisWith("", home+"/.dotfiles")) // dropped by RepoKey

	groups := GroupByRepo(as, 10, map[string]string{"terminal-app": "tmux-ctrl"})
	if len(groups["client-project"]) != 12 {
		t.Errorf("client-project = %d, want 12", len(groups["client-project"]))
	}
	if len(groups["tmux-ctrl"]) != 11 {
		t.Errorf("tmux-ctrl = %d, want 11 (8 configured + 3 terminal-app fold)", len(groups["tmux-ctrl"]))
	}
	if _, ok := groups[""]; ok {
		t.Error("empty key must never appear as a group")
	}
	if _, ok := groups["src"]; ok {
		t.Error("basename(cwd) junk repo 'src' must never appear")
	}
}
