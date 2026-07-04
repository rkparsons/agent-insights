package insightseval

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// EnvPin is the composed nested-claude environment for one eval run: an
// ephemeral config dir (pristine snapshot + live skills overlaid), an empty
// scratch cwd, and the hashes that make the run reproducible.
type EnvPin struct {
	ConfigDir        string
	WorkDir          string
	ClaudeVersion    string
	SnapshotHash     string
	EnvHash          string
	SkillHashes      map[string]string
	SnapshotWarnings []string
	DriftWarnings    []string
}

// ComposeEnvPin builds the per-run nested-claude environment. The snapshot
// checkout itself is never used as CLAUDE_CONFIG_DIR — claude writes runtime
// state into its config dir on every invocation, which would dirty the
// append-only fixture repo and drift any hash taken from a live dir.
// claudeVersion is injected so tests never exec the real binary.
func ComposeEnvPin(dataDir, scratchDir string, skillDirs map[string]string, claudeVersion string) (EnvPin, error) {
	pristine := filepath.Join(dataDir, "config-snapshot", "global")
	pin := EnvPin{
		ConfigDir:     filepath.Join(scratchDir, "config"),
		WorkDir:       filepath.Join(scratchDir, "cwd"),
		ClaudeVersion: claudeVersion,
		SkillHashes:   map[string]string{},
	}
	snapHash, err := hashTree(pristine)
	if err != nil {
		return pin, fmt.Errorf("hash config snapshot: %w", err)
	}
	pin.SnapshotHash = snapHash
	pin.EnvHash = cacheKey("env", claudeVersion, snapHash)

	if _, err := copyTree(pristine, pin.ConfigDir, func(string) bool { return true }); err != nil {
		return pin, fmt.Errorf("copy config snapshot: %w", err)
	}
	names := make([]string, 0, len(skillDirs))
	for name := range skillDirs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		dir := skillDirs[name]
		h, err := hashTree(dir)
		if err != nil {
			return pin, fmt.Errorf("hash skill %s: %w", name, err)
		}
		pin.SkillHashes[name] = h
		dst := filepath.Join(pin.ConfigDir, "skills", name)
		// The live skill replaces the frozen copy wholesale — it is the
		// variable under test.
		if err := os.RemoveAll(dst); err != nil {
			return pin, err
		}
		if _, err := copyTree(dir, dst, func(string) bool { return true }); err != nil {
			return pin, fmt.Errorf("overlay skill %s: %w", name, err)
		}
	}
	if err := os.MkdirAll(pin.WorkDir, 0o755); err != nil {
		return pin, err
	}

	pin.DriftWarnings = liveConfigDrift(dataDir)

	pin.SnapshotWarnings, err = snapshotSkills(dataDir, skillDirs, pin.SkillHashes)
	if err != nil {
		return pin, err
	}
	return pin, nil
}

// liveConfigDrift compares the live hook/statusline files against the frozen
// copies. settings.json may reference these by absolute live path, which
// CLAUDE_CONFIG_DIR cannot redirect — drift here is unpinnable but must be
// visible.
func liveConfigDrift(dataDir string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var warnings []string
	pairs := []struct{ live, frozen, label string }{
		{filepath.Join(home, ".claude", "hooks"), filepath.Join(dataDir, "config-snapshot", "global", "hooks"), "hooks"},
		{filepath.Join(home, ".claude", "statusline.mjs"), filepath.Join(dataDir, "config-snapshot", "global", "statusline.mjs"), "statusline.mjs"},
	}
	for _, p := range pairs {
		liveHash, liveErr := hashPath(p.live)
		frozenHash, frozenErr := hashPath(p.frozen)
		if liveErr != nil || frozenErr != nil {
			continue // absent on either side: nothing to compare
		}
		if liveHash != frozenHash {
			warnings = append(warnings, fmt.Sprintf("live ~/.claude/%s differs from frozen config-snapshot copy (unpinnable: settings.json may reference it by absolute path)", p.label))
		}
	}
	return warnings
}

// hashPath hashes a file or a directory tree uniformly.
func hashPath(p string) (string, error) {
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return hashTree(p)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return sha256hex(raw), nil
}

// hashTree content-hashes a directory tree: sorted relative paths + sizes +
// bytes. Symlinks are followed exactly like the freeze's copyTree (root
// resolved up front; symlinked directories found deeper in the tree recursed
// with cycle detection) so a skill hash always covers what a snapshot copy
// would copy.
func hashTree(root string) (string, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	files := map[string]string{} // rel path → abs path
	if err := collectTreeFiles(resolved, "", files, map[string]bool{resolved: true}); err != nil {
		return "", err
	}
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	h := sha256.New()
	for _, rel := range rels {
		data, err := os.ReadFile(files[rel])
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// collectTreeFiles mirrors copyTreeVisited's symlink handling for hashing:
// broken symlinks skipped, symlinked dirs recursed once (visited set breaks
// cycles).
func collectTreeFiles(root, prefix string, files map[string]string, visited map[string]bool) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		if d.Type()&fs.ModeSymlink != 0 {
			info, statErr := os.Stat(p)
			if statErr != nil {
				return nil // broken symlink
			}
			if info.IsDir() {
				target, evalErr := filepath.EvalSymlinks(p)
				if evalErr != nil {
					return nil
				}
				if visited[target] {
					return nil
				}
				visited[target] = true
				return collectTreeFiles(target, filepath.Join(prefix, rel), files, visited)
			}
		}
		files[filepath.Join(prefix, rel)] = p
		return nil
	})
}

// claudeVersionString asks the real binary. Only the CLI path calls this;
// everything else receives the version as a parameter.
func claudeVersionString() (string, error) {
	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("claude --version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// snapshotSkills stores a content-addressed copy of each skill dir in the
// data repo on first sight of a new hash, so verdict-recorded hashes stay
// resolvable after the live skill moves on. Returns formatted warnings, one
// per skill snapshotted this run, distinguishing a first-ever snapshot from a
// change since the last one (prior snapshots detected by glob).
func snapshotSkills(dataDir string, skillDirs, hashes map[string]string) ([]string, error) {
	var warnings []string
	names := make([]string, 0, len(skillDirs))
	for name := range skillDirs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		h := hashes[name]
		dst := filepath.Join(dataDir, "skill-snapshots", h, name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		prior, _ := filepath.Glob(filepath.Join(dataDir, "skill-snapshots", "*", name))
		resolved, err := filepath.EvalSymlinks(skillDirs[name])
		if err != nil {
			return warnings, err
		}
		if _, err := copyTree(resolved, dst, func(string) bool { return true }); err != nil {
			return warnings, fmt.Errorf("snapshot skill %s: %w", name, err)
		}
		if len(prior) > 0 {
			warnings = append(warnings, fmt.Sprintf("skill %s changed since its last snapshot (new hash stored in skill-snapshots/)", name))
		} else {
			warnings = append(warnings, fmt.Sprintf("skill %s: first snapshot stored in skill-snapshots/", name))
		}
	}
	return warnings, nil
}

// ClaudeVersionString is claudeVersionString exported for the CLI.
func ClaudeVersionString() (string, error) { return claudeVersionString() }
