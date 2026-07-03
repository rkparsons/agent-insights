package insightseval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyFileRawAppendOnly(t *testing.T) {
	dir := t.TempDir()
	src := writeTemp(t, dir, "a.json", `{"x":1}`)
	dst := filepath.Join(dir, "out", "a.json")
	if err := copyFileRaw(src, dst); err != nil {
		t.Fatal(err)
	}
	if err := copyFileRaw(src, dst); err != nil {
		t.Fatalf("idempotent re-copy: %v", err)
	}
	src2 := writeTemp(t, dir, "b.json", `{"x":2}`)
	err := copyFileRaw(src2, dst)
	if err == nil || !strings.Contains(err.Error(), "append-only violation") {
		t.Fatalf("want append-only violation, got %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"x":1}` {
		t.Fatalf("content = %q", got)
	}
}

func TestSnapshotConfigCopiesGlobalAndRepoSurface(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := t.TempDir()
	data := t.TempDir()
	mustWrite := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(filepath.Join(home, ".claude", "CLAUDE.md"), "global rules")
	mustWrite(filepath.Join(home, ".claude", "RTK.md"), "rtk include")
	mustWrite(filepath.Join(home, ".claude", "settings.json"), "{}")
	mustWrite(filepath.Join(home, ".claude", "skills", "synthesizing-workflow-insights", "SKILL.md"), "skill body")
	mustWrite(filepath.Join(repo, "CLAUDE.md"), "repo rules")
	mustWrite(filepath.Join(repo, ".claude", "settings.json"), "{}")

	n, err := SnapshotConfig(data, map[string]BucketPopulations{
		"myrepo":   {RepoPath: repo},
		"pathless": {}, // no RepoPath → skipped without error
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Fatalf("copied = %d, want 6", n)
	}
	for _, p := range []string{
		filepath.Join(data, "config-snapshot", "global", "CLAUDE.md"),
		filepath.Join(data, "config-snapshot", "global", "RTK.md"),
		filepath.Join(data, "config-snapshot", "global", "settings.json"),
		filepath.Join(data, "config-snapshot", "global", "skills", "synthesizing-workflow-insights", "SKILL.md"),
		filepath.Join(data, "config-snapshot", "repos", "myrepo", "CLAUDE.md"),
		filepath.Join(data, "config-snapshot", "repos", "myrepo", ".claude", "settings.json"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing %s", p)
		}
	}
}

func TestEnsureRepoScaffold(t *testing.T) {
	data := t.TempDir()
	if err := EnsureRepoScaffold(data); err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile(filepath.Join(data, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "PRIVATE") {
		t.Fatal("README must warn the repo is private fixture data")
	}
	// existing README is left alone
	if err := os.WriteFile(filepath.Join(data, "README.md"), []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRepoScaffold(data); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(data, "README.md"))
	if string(got) != "custom" {
		t.Fatal("scaffold overwrote an existing README")
	}
}
