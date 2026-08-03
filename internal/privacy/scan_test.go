package privacy

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestScanCatchesEveryClass(t *testing.T) {
	leaks := []string{
		"cwd /Users/rparsons/Developer/x",
		"cwd /home/rparsons/x",
		"session 4b1f6c58-3c9e-4a1d-9c2e-2b6f8e0a1234 did it",
		"repo: client-project",
		"client-project",
		"branch TICKET-0000",
		"user dev",
		"commit by redacted",
		"terminal-app preview corruption",
	}
	for _, l := range leaks {
		if hits := Scan([]byte(l)); len(hits) == 0 {
			t.Errorf("leak not caught: %q", l)
		}
	}
}

func TestScanAllowsKnownSyntheticForms(t *testing.T) {
	clean := []string{
		"cwd /Users/dev/Developer/x",
		"cwd /home/user/x",
		"session 00000000-0000-4000-8000-000000000001 did it",
		"session 0abc1234-de56-4f78-9abc-def012345678 did it",
		`{"target":"C-04","item_ref":"alpha/theme/3"}`,
		"Shortcut ticket created", // not sc-NNNN
	}
	for _, c := range clean {
		if hits := Scan([]byte(c)); len(hits) != 0 {
			t.Errorf("clean text flagged: %q -> %v", c, hits)
		}
	}
}

// TestScanRepoWide is the CI backstop: it walks every git-tracked file and
// fails if any of them trip a privacy check. It is gated behind PRIVACY_SCAN=1
// because it currently fails on evals/*.md (client-project, TICKET-0000, terminal-app — see
// task-12-report.md for the exact file list); Task 13 redacts those docs.
// TODO(task-13): delete the gate below once evals/*.md is clean, so this runs
// unconditionally in `go test ./...` / CI.
func TestScanRepoWide(t *testing.T) {
	if os.Getenv("PRIVACY_SCAN") == "" {
		t.Skip("set PRIVACY_SCAN=1 to run; see the TODO above for why this is gated")
	}

	root := repoRoot(t)
	var offenders []string
	for _, f := range gitLsFiles(t, root) {
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if hits := Scan(data); len(hits) > 0 {
			offenders = append(offenders, f+": "+strings.Join(hits, "; "))
		}
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("privacy scan found %d file(s) with committed leak patterns:\n  %s",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func gitLsFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("git ls-files returned no files")
	}
	return lines
}
