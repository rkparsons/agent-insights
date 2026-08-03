package eval

import "testing"

func TestPrivacyScanCatchesEveryClass(t *testing.T) {
	leaks := []string{
		"session 0abc1234-de56-4f78-9abc-def012345678 did it", // session id
		"path /Users/dev/x", // cwd/home
		"path /home/user/x",
		"under $HOME/.claude",
		"branch sc-42",             // ticket-branch marker
		"repo/.worktrees/insights", // worktree path
		"session 0ABC1234-DE56-4F78-9ABC-DEF012345678 did it", // uppercase-hex session id
		"branch SC-42",
	}
	for _, l := range leaks {
		if hits := privacyScan([]byte(l)); len(hits) == 0 {
			t.Errorf("leak not caught: %q", l)
		} else {
			for _, h := range hits {
				if h == l {
					t.Errorf("scan finding restates the leak: %q", h)
				}
			}
		}
	}
	clean := `{"target":"C-04","granularity":"partial","item_ref":"alpha/theme/3","hash":"0836c26e39ae4d35bc062471a187ce55deadbeef"}`
	if hits := privacyScan([]byte(clean)); len(hits) != 0 {
		t.Errorf("clean verdict flagged: %v", hits)
	}
}
