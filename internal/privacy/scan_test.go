package privacy

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestScanCatchesEveryGenericClass(t *testing.T) {
	s := New(nil)
	leaks := []string{
		"cwd /Users/rparsons/Developer/x",
		"cwd /Users/Richard2/Developer/x", // mixed-case + digit, a real username shape
		"cwd /home/rparsons/x",
		"projects/-Users-rparsons-Developer-x",       // dash-encoded slug, no slash anywhere
		"projects/-Users-r-Developer-x",              // single-letter placeholder is still not /Users/dev
		"projects/-home-rparsons-x",                  // dash-encoded /home sibling
		"tmp/-private-tmp--Users-r--worktrees-y-scr", // slug embedded mid-path
		"session 4b1f6c58-3c9e-4a1d-9c2e-2b6f8e0a1234 did it",
		"branch sc-99999",
	}
	for _, l := range leaks {
		if hits := s.Scan([]byte(l)); len(hits) == 0 {
			t.Errorf("leak not caught: %q", l)
		}
	}
}

func TestScanAllowsKnownSyntheticForms(t *testing.T) {
	s := New(nil)
	clean := []string{
		"cwd /Users/dev/Developer/x",
		"cwd /Users/DEV/Developer/x", // case-insensitive match on the allowed placeholder too
		"cwd /home/user/x",
		"projects/-Users-dev-Developer-x",
		"projects/-Users-DEV-Developer-x",
		"projects/-home-user-x",
		"session 00000000-0000-4000-8000-000000000001 did it",
		"session 0abc1234-de56-4f78-9abc-def012345678 did it",
		`{"target":"C-04","item_ref":"alpha/theme/3"}`,
		"Shortcut ticket created", // not sc-NNNN
		"a misc-1234 bugfix",      // "sc-" substring, not the sc-NNNN marker (word boundary)
		"see desc-20260803.md",
	}
	for _, c := range clean {
		if hits := s.Scan([]byte(c)); len(hits) != 0 {
			t.Errorf("clean text flagged: %q -> %v", c, hits)
		}
	}
}

// TestPrivatePatternsFromFile covers the identity layer with synthetic tokens:
// the real ones are exactly what must not be committed here, so the test
// writes its own pattern file and points the loader at it.
func TestPrivatePatternsFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, PrivatePatternsFile)
	body := "# identity tokens, one regexp per line\n\nacme-corp\nwidgets\\.example\n\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	pats, err := LoadPrivatePatterns(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(pats) != 2 {
		t.Fatalf("loaded %d patterns, want 2 (blank lines and # comments skipped)", len(pats))
	}
	s := New(pats)
	for _, leak := range []string{"repo: acme-corp", "ACME-CORP", "mail widgets.example"} {
		hits := s.Scan([]byte(leak))
		if len(hits) == 0 {
			t.Errorf("private pattern missed %q", leak)
			continue
		}
		for _, h := range hits {
			if strings.Contains(strings.ToLower(h), "acme") || strings.Contains(h, "widgets") {
				t.Errorf("finding label restates the private pattern: %q", h)
			}
		}
	}
	if hits := s.Scan([]byte("repo: unrelated-corp")); len(hits) != 0 {
		t.Errorf("clean text flagged by private patterns: %v", hits)
	}
	// The generic classes must survive the composition, not be replaced by it.
	if hits := s.Scan([]byte("cwd /Users/rparsons/x")); len(hits) != 1 {
		t.Errorf("generic classes lost when private patterns load: %v", hits)
	}
}

func TestLoadPrivatePatternsAbsentIsNotAnError(t *testing.T) {
	pats, err := LoadPrivatePatterns(t.TempDir())
	if err != nil || pats != nil {
		t.Fatalf("absent file: got (%v, %v), want (nil, nil) — a fresh clone has none", pats, err)
	}
}

func TestLoadPrivatePatternsRejectsMalformedLineWithoutEchoingIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, PrivatePatternsFile), []byte("ok\nacme(corp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadPrivatePatterns(dir)
	if err == nil {
		t.Fatal("malformed pattern must be an error")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error should name the line number, got %q", err)
	}
	if strings.Contains(err.Error(), "acme") {
		t.Errorf("error echoes the pattern content: %q", err)
	}
}

// selfScanExempt names the only file excluded from the repo-wide walk.
// scan_test.go's fixtures above are, necessarily, real examples of every
// generic leak class — it always trips Scan on itself. scan.go is deliberately
// NOT exempt: now that identity tokens live in the gitignored pattern file, the
// scanner's own source contains nothing but shapes and allowed placeholders, so
// it holds itself to the same bar as every other committed file.
var selfScanExempt = map[string]bool{
	"internal/privacy/scan_test.go": true,
}

// TestScanRepoWide is the CI backstop: it walks every git-tracked file and
// fails if any of them trip a privacy check. Unconditional — it runs in a
// plain `go test ./...`, so a leak can never be committed without a red test.
func TestScanRepoWide(t *testing.T) {
	root := repoRoot(t)
	pats, err := LoadPrivatePatterns(root)
	if err != nil {
		t.Fatalf("load private patterns: %v", err)
	}
	// Count and basename only: the resolved path is an absolute home path, and
	// this test's whole job is keeping those out of logs.
	t.Logf("private identity patterns loaded: %d (from %s)", len(pats), filepath.Base(PrivatePatternsPath(root)))
	s := New(pats)

	var offenders []string
	for _, f := range gitLsFiles(t, root) {
		if f == PrivatePatternsFile {
			t.Errorf("%s is tracked — the private pattern file must stay gitignored", f)
			continue
		}
		if selfScanExempt[f] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if hits := s.Scan(data); len(hits) > 0 {
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
