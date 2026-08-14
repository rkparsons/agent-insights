package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rkparsons/agent-insights/internal/insights"
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

// TestCopyTreeUnreadableSubdirErrors covers finding C: a subtree read error
// must propagate, not be swallowed into a silent partial freeze.
func TestCopyTreeUnreadableSubdirErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission bits are not enforced")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	blocked := filepath.Join(src, "blocked")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blocked, 0o755) })

	if _, err := copyTree(src, filepath.Join(dir, "dst"), func(string) bool { return true }); err == nil {
		t.Fatal("want error for unreadable subdir, got nil")
	}
}

// TestCopyTreeMissingRootReturnsNilError guards the restructured root-missing
// check: a missing srcRoot copies nothing and is not an error.
func TestCopyTreeMissingRootReturnsNilError(t *testing.T) {
	dir := t.TempDir()
	n, err := copyTree(filepath.Join(dir, "does-not-exist"), filepath.Join(dir, "dst"), func(string) bool { return true })
	if err != nil {
		t.Fatalf("missing root must not error: %v", err)
	}
	if n != 0 {
		t.Fatalf("n = %d, want 0", n)
	}
}

// TestCopyTreeSymlinkedSrcRootCopiesContents covers finding C: a srcRoot that
// is itself a symlink-to-dir must copy its contents, not silently copy zero
// files (the visited-set seed used to pre-mark the resolved target before the
// walk ever reached it).
func TestCopyTreeSymlinkedSrcRootCopiesContents(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "f.txt"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "dst")
	n, err := copyTree(link, dst, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("copied = %d, want 1 (symlinked srcRoot must not copy zero files)", n)
	}
	got, err := os.ReadFile(filepath.Join(dst, "f.txt"))
	if err != nil {
		t.Fatalf("nested file missing: %v", err)
	}
	if string(got) != "body" {
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
	mustWrite(filepath.Join(home, ".claude", "statusline.mjs"), "statusline body")
	mustWrite(filepath.Join(home, ".claude", "skills", "synthesizing-workflow-insights", "SKILL.md"), "skill body")
	mustWrite(filepath.Join(home, ".claude", "hooks", "pre-commit.sh"), "hook body")
	mustWrite(filepath.Join(home, ".claude", "hooks", "nested", "deep.sh"), "nested hook body")
	mustWrite(filepath.Join(home, ".claude", "plugins", "config.json"), "{}")
	mustWrite(filepath.Join(home, ".claude", "plugins", "known_marketplaces.json"), "{}")
	// never the plugins cache
	mustWrite(filepath.Join(home, ".claude", "plugins", "cache", "some-plugin", "index.js"), "cached plugin code")
	mustWrite(filepath.Join(repo, "CLAUDE.md"), "repo rules")
	mustWrite(filepath.Join(repo, ".claude", "settings.json"), "{}")

	n, err := SnapshotConfig(data, map[string]BucketPopulations{
		"myrepo":   {RepoPath: repo},
		"pathless": {}, // no RepoPath and no configured root → skipped without error
	}, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 {
		t.Fatalf("copied = %d, want 11", n)
	}
	for _, p := range []string{
		filepath.Join(data, "config-snapshot", "global", "CLAUDE.md"),
		filepath.Join(data, "config-snapshot", "global", "RTK.md"),
		filepath.Join(data, "config-snapshot", "global", "settings.json"),
		filepath.Join(data, "config-snapshot", "global", "statusline.mjs"),
		filepath.Join(data, "config-snapshot", "global", "skills", "synthesizing-workflow-insights", "SKILL.md"),
		filepath.Join(data, "config-snapshot", "global", "hooks", "pre-commit.sh"),
		filepath.Join(data, "config-snapshot", "global", "hooks", "nested", "deep.sh"),
		filepath.Join(data, "config-snapshot", "global", "plugins", "config.json"),
		filepath.Join(data, "config-snapshot", "global", "plugins", "known_marketplaces.json"),
		filepath.Join(data, "config-snapshot", "repos", "myrepo", "CLAUDE.md"),
		filepath.Join(data, "config-snapshot", "repos", "myrepo", ".claude", "settings.json"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing %s", p)
		}
	}
	if _, err := os.Stat(filepath.Join(data, "config-snapshot", "global", "plugins", "cache")); !os.IsNotExist(err) {
		t.Fatal("plugins cache must never be snapshotted")
	}
}

// The configured checkout root is what the production manifest names, so the
// frozen copy must come from there — a bucket's RepoPath is only an observed
// session cwd, and is the fallback for repos the config does not list.
func TestSnapshotConfigPrefersConfiguredRepoRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	data := t.TempDir()
	configured := filepath.Join(t.TempDir(), "alpha")
	observed := filepath.Join(t.TempDir(), "alpha")
	unconfigured := filepath.Join(t.TempDir(), "beta")
	for path, body := range map[string]string{
		filepath.Join(configured, "CLAUDE.md"):   "configured root rules",
		filepath.Join(observed, "CLAUDE.md"):     "stale checkout rules",
		filepath.Join(unconfigured, "CLAUDE.md"): "beta rules",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := SnapshotConfig(data, map[string]BucketPopulations{
		"alpha": {RepoPath: observed},
		"beta":  {RepoPath: unconfigured},
	}, insights.Config{Repos: []string{configured}}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(data, "config-snapshot", "repos", "alpha", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "configured root rules" {
		t.Fatalf("frozen alpha CLAUDE.md = %q, want the configured root's", got)
	}
	got, err = os.ReadFile(filepath.Join(data, "config-snapshot", "repos", "beta", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("an unconfigured bucket must still freeze from its observed path: %v", err)
	}
	if string(got) != "beta rules" {
		t.Fatalf("frozen beta CLAUDE.md = %q", got)
	}
}

// A repo that simply has no CLAUDE.md and no .claude tree is not an
// unavailable repo: the model must be able to look at that root and find
// nothing, which is production's answer to the asset-ladder question. Only an
// unresolvable root (no path, or a path that is not there) is omitted.
func TestSnapshotConfigFreezesAssetlessRepoAsEmptyDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	data := t.TempDir()
	assetless := filepath.Join(t.TempDir(), "alpha")
	if err := os.MkdirAll(assetless, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetless, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := SnapshotConfig(data, map[string]BucketPopulations{
		"alpha": {RepoPath: assetless},
		"beta":  {RepoPath: filepath.Join(t.TempDir(), "gone")}, // resolvable path, absent on disk
	}, insights.Config{}); err != nil {
		t.Fatal(err)
	}
	alphaDst := filepath.Join(data, "config-snapshot", "repos", "alpha")
	if _, err := os.Stat(filepath.Join(alphaDst, frozenEmptyRepoMarker)); err != nil {
		t.Fatalf("an assetless repo must freeze as a real (git-retainable) dir: %v", err)
	}
	entries, err := os.ReadDir(alphaDst)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the marker must be the only thing in it: %v %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(data, "config-snapshot", "repos", "beta")); !os.IsNotExist(err) {
		t.Fatalf("an absent repo root must freeze nothing: %v", err)
	}

	// the manifest consequence: alpha is a readable root, beta is unavailable
	cfg, warnings := frozenAssetConfig(data, []string{"alpha", "beta"}, "claude-fable-5")
	if len(cfg.Repos) != 1 || filepath.Base(cfg.Repos[0]) != "alpha" {
		t.Fatalf("assetless-but-resolvable must render as a real root: %v", cfg.Repos)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "beta") {
		t.Fatalf("only the unresolvable root warns: %v", warnings)
	}

	// re-freezing is a no-op, and a repo that GAINS assets later keeps the
	// frozen (append-only) state rather than mixing a marker with content
	if err := os.WriteFile(filepath.Join(assetless, "CLAUDE.md"), []byte("new rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SnapshotConfig(data, map[string]BucketPopulations{"alpha": {RepoPath: assetless}}, insights.Config{}); err != nil {
		t.Fatalf("a repo that gained assets must not break the append-only freeze: %v", err)
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
