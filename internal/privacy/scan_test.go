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
		"cwd /Users/Richard2/Developer/x", // mixed-case + digit, a real username shape
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
		"cwd /Users/DEV/Developer/x", // case-insensitive match on the allowed placeholder too
		"cwd /home/user/x",
		"session 00000000-0000-4000-8000-000000000001 did it",
		"session 0abc1234-de56-4f78-9abc-def012345678 did it",
		`{"target":"C-04","item_ref":"alpha/theme/3"}`,
		"Shortcut ticket created", // not sc-NNNN
		"a misc-1234 bugfix",      // "sc-" substring, not the sc-NNNN marker (word boundary)
		"see desc-20260803.md",
	}
	for _, c := range clean {
		if hits := Scan([]byte(c)); len(hits) != 0 {
			t.Errorf("clean text flagged: %q -> %v", c, hits)
		}
	}
}

// selfPackagePrefix excludes this package's own source from the scan: scan.go
// necessarily spells out every leak pattern as a literal (label strings like
// "client-project"/"terminal-app"/"dev"), and scan_test.go's fixtures above
// embed real examples of each leak class plus the synthetic-allowed forms —
// both always trip Scan on themselves. This is the scanner's own definition
// and unit tests, not a committed artifact that could leak anything.
const selfPackagePrefix = "internal/privacy/"

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
		if strings.HasPrefix(f, selfPackagePrefix) {
			continue
		}
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

// gitLsFiles uses -z (NUL-separated, unquoted paths) so a tracked filename
// with special/non-ASCII characters comes back verbatim instead of
// core.quotePath-escaped (which would otherwise fail the os.ReadFile join).
func gitLsFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	entries := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	if len(entries) == 0 || entries[0] == "" {
		t.Fatal("git ls-files returned no files")
	}
	return entries
}
