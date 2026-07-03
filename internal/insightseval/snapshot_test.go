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

func TestCopyFileRawRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	src := writeTemp(t, dir, "source.json", `{"x":1}`)
	dstParent := filepath.Join(dir, "dest")
	if err := os.MkdirAll(dstParent, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dstParent, "subdir")
	// Create a directory at the dst path
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	// copyFileRaw should reject dst when it's a directory
	err := copyFileRaw(src, dst)
	if err == nil {
		t.Fatal("expected error when dst is a directory, got nil")
	}
	// verify it's still a directory
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatal("dst should still be a directory")
	}
}

func TestCopyTreeFollowsSymlinkedDirectory(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	realSkillDir := filepath.Join(dir, "real-elsewhere", "myskill")
	if err := os.MkdirAll(realSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realSkillDir, "SKILL.md"), []byte("skill body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realSkillDir, filepath.Join(src, "skills", "myskill")); err != nil {
		t.Fatal(err)
	}

	// symlinked regular file
	realFile := filepath.Join(dir, "real.md")
	if err := os.WriteFile(realFile, []byte("real body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realFile, filepath.Join(src, "linked.md")); err != nil {
		t.Fatal(err)
	}

	// broken symlink
	if err := os.Symlink(filepath.Join(src, "does-not-exist"), filepath.Join(src, "broken")); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "dst")
	var seenRels []string
	n, err := copyTree(src, dst, func(rel string) bool {
		seenRels = append(seenRels, rel)
		return strings.HasPrefix(rel, "skills"+string(filepath.Separator)) || rel == "linked.md"
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("copied = %d, want 2", n)
	}
	wantNested := filepath.Join("skills", "myskill", "SKILL.md")
	found := false
	for _, r := range seenRels {
		if r == wantNested {
			found = true
		}
	}
	if !found {
		t.Fatalf("keep filter never saw %q, saw %v", wantNested, seenRels)
	}
	got, err := os.ReadFile(filepath.Join(dst, "skills", "myskill", "SKILL.md"))
	if err != nil {
		t.Fatalf("nested symlinked-dir file missing: %v", err)
	}
	if string(got) != "skill body" {
		t.Fatalf("content = %q", got)
	}
	if _, err := os.ReadFile(filepath.Join(dst, "linked.md")); err != nil {
		t.Fatalf("symlinked file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "broken")); !os.IsNotExist(err) {
		t.Fatal("broken symlink should be skipped silently, not copied")
	}
}

func TestCopyTreeSymlinkCycleTerminates(t *testing.T) {
	// Create a temp directory structure with a symlink cycle
	tmpSrc := t.TempDir()
	tmpDst := t.TempDir()

	// Create subdirectory and file
	subdir := filepath.Join(tmpSrc, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a normal file to ensure non-cycle files are copied
	if err := os.WriteFile(filepath.Join(subdir, "file.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink from subdir back to tmpSrc (ancestor cycle)
	linkPath := filepath.Join(subdir, "cycle")
	if err := os.Symlink(tmpSrc, linkPath); err != nil {
		t.Fatal(err)
	}

	// This should complete without hanging and without error
	n, err := copyTree(tmpSrc, tmpDst, func(rel string) bool {
		return true
	})

	if err != nil {
		t.Errorf("copyTree returned error: %v", err)
	}

	// Verify the normal file was copied
	copiedFile := filepath.Join(tmpDst, "subdir", "file.txt")
	if _, err := os.Stat(copiedFile); err != nil {
		t.Errorf("expected file not copied: %v", err)
	}

	// Verify at least one file was copied (the normal file)
	if n < 1 {
		t.Errorf("expected at least 1 file copied, got %d", n)
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
