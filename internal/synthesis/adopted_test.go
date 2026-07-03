package synthesis

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestAdoptChecker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("Never add comments that restate what the code does."), 0o644); err != nil {
		t.Fatal(err)
	}
	check := newAdoptCheckerFromFiles([]string{filepath.Join(dir, "CLAUDE.md")})
	if got := check(Recommendation{Statement: "Avoid comments that restate what the code does"}); got != "yes" {
		t.Errorf("adopted rule → %q, want yes", got)
	}
	if got := check(Recommendation{Statement: "Prefer trunk-based development with short-lived branches"}); got != "no" {
		t.Errorf("unadopted rule → %q, want no", got)
	}
}

func TestAdoptPathsListsCheckerCorpus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := t.TempDir()
	mustWrite := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(filepath.Join(home, ".claude", "CLAUDE.md"), "global")
	mustWrite(filepath.Join(home, ".claude", "skills", "x", "SKILL.md"), "skill")
	mustWrite(filepath.Join(repo, ".claude", "rules.md"), "repo rules")

	paths := AdoptPaths(repo)
	want := []string{
		filepath.Join(repo, "CLAUDE.md"),
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(repo, ".claude", "rules.md"),
		filepath.Join(home, ".claude", "skills", "x", "SKILL.md"),
	}
	if !slices.Equal(paths, want) {
		t.Fatalf("AdoptPaths = %v, want %v", paths, want)
	}
}
