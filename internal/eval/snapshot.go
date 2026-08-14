package eval

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
)

// copyFileRaw copies src to dst uncompressed with the same append-only
// contract as freezeFile: identical existing content is a silent skip,
// differing content an error.
func copyFileRaw(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile(dst); err == nil {
		if bytes.Equal(existing, raw) {
			return nil
		}
		return fmt.Errorf("append-only violation: %s exists with different content", dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("unable to check existing file %s: %w", dst, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}

// copyTreeVisited is the recursive implementation of copyTree with cycle detection.
func copyTreeVisited(srcRoot, dstRoot string, keep func(string) bool, visited map[string]bool) (int, error) {
	n := 0
	err := filepath.WalkDir(srcRoot, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(srcRoot, p)
		if relErr != nil {
			return nil
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
				// Skip if we've already visited this target (cycle detection)
				if visited[target] {
					return nil
				}
				visited[target] = true
				sub, subErr := copyTreeVisited(target, filepath.Join(dstRoot, rel), func(inner string) bool {
					return keep(filepath.Join(rel, inner))
				}, visited)
				if subErr != nil {
					return subErr
				}
				n += sub
				return nil
			}
			// symlink to a regular file: os.ReadFile below follows it as usual.
		}
		if !keep(rel) {
			return nil
		}
		if err := copyFileRaw(p, filepath.Join(dstRoot, rel)); err != nil {
			return err
		}
		n++
		return nil
	})
	return n, err
}

// copyTree copies every file under srcRoot accepted by keep into dstRoot,
// preserving relative paths. A missing srcRoot copies nothing; any other
// error reading the root or a subtree (e.g. a permission-denied directory)
// propagates rather than being swallowed into a silent partial freeze.
//
// srcRoot is resolved with EvalSymlinks before walking: WalkDir Lstats
// entries, so a symlinked directory passed directly as root (e.g. a
// stow-managed ~/.claude/skills/* entry, or a repo whose whole srcRoot is a
// symlink) would otherwise arrive as a non-dir leaf and WalkDir would never
// follow it — recursing on the symlink path itself would re-Lstat the same
// non-dir leaf forever. Resolving up front sidesteps that; keep still sees
// paths relative to the walk root, so prefix filters keep working the same
// whether srcRoot was a symlink or not. Symlinked directories found DEEPER in
// the tree are still handled by the recursion inside copyTreeVisited. A
// broken symlink (target Stat fails) is skipped silently.
//
// Cycles (symlink to self or ancestor) are detected via an EvalSymlinks-resolved
// visited set, preventing unbounded recursion.
func copyTree(srcRoot, dstRoot string, keep func(rel string) bool) (int, error) {
	if _, err := os.Stat(srcRoot); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	resolved := srcRoot
	if evalPath, err := filepath.EvalSymlinks(srcRoot); err == nil {
		resolved = evalPath
	}
	visited := map[string]bool{resolved: true}
	return copyTreeVisited(resolved, dstRoot, keep, visited)
}

// CopyGroundTruth freezes the live synthesis artifacts into ground-truth/: the
// v2 global snapshots under global/ (the anchor source under the v2 contract)
// and any v1 per-repo reports still on disk beside them, which stay readable
// for historical records. loadGroundTruth reads the latter and skips global/;
// loadGlobalGroundTruth reads the former — so the snapshot dir is never carded
// as a phantom repo bucket.
//
// Failed-run model output is NOT a concern here: it never passed the verifier's
// privacy scan and so lives outside Dir() entirely (synthesis.diagnosticsDir).
func CopyGroundTruth(dataDir string) (int, error) {
	return copyTree(synthesis.Dir(), filepath.Join(dataDir, "ground-truth"), func(string) bool { return true })
}

// CopyBaselinePool freezes the analyses pool as baseline-pool/v1 — the judged
// fields that produced the ground truth. Call only when assertions are clean.
func CopyBaselinePool(dataDir string) (int, error) {
	return copyTree(insights.AnalysesDir(), filepath.Join(dataDir, "baseline-pool", "v1"), func(rel string) bool {
		return strings.HasSuffix(rel, ".json")
	})
}

// SnapshotConfig freezes the config surface the synthesis manifest points the
// model at and the env-pinning later composes from: the global ~/.claude surface (top-level
// *.md, settings.json, statusline.mjs, the full skills and hooks trees, and
// the plugin inventory — never the plugins cache) plus each bucket repo's
// CLAUDE.md and .claude tree.
func SnapshotConfig(dataDir string, buckets map[string]BucketPopulations) (int, error) {
	home, _ := os.UserHomeDir()
	total := 0
	globalDst := filepath.Join(dataDir, "config-snapshot", "global")
	n, err := copyTree(filepath.Join(home, ".claude"), globalDst, func(rel string) bool {
		if strings.HasPrefix(rel, "skills"+string(filepath.Separator)) {
			return true
		}
		if strings.HasPrefix(rel, "hooks"+string(filepath.Separator)) {
			return true
		}
		if rel == filepath.Join("plugins", "config.json") || rel == filepath.Join("plugins", "known_marketplaces.json") {
			return true
		}
		if !strings.Contains(rel, string(filepath.Separator)) {
			return strings.HasSuffix(rel, ".md") || rel == "settings.json" || rel == "statusline.mjs"
		}
		return false
	})
	if err != nil {
		return total, err
	}
	total += n

	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		repoPath := buckets[k].RepoPath
		if repoPath == "" {
			continue
		}
		repoDst := filepath.Join(dataDir, "config-snapshot", "repos", k)
		if _, err := os.Stat(filepath.Join(repoPath, "CLAUDE.md")); err == nil {
			if err := copyFileRaw(filepath.Join(repoPath, "CLAUDE.md"), filepath.Join(repoDst, "CLAUDE.md")); err != nil {
				return total, err
			}
			total++
		}
		n, err := copyTree(filepath.Join(repoPath, ".claude"), filepath.Join(repoDst, ".claude"), func(rel string) bool {
			base := filepath.Base(rel)
			return strings.HasSuffix(rel, ".md") || base == "settings.json" || base == "settings.local.json"
		})
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

const scaffoldReadme = `# insights-eval-data

PRIVATE fixture data for the agent-insights outcome-eval suite. Contains
raw Claude Code session transcripts (including work content) — never make this
repo public and never copy its contents into a shareable artifact.

Produced and verified by ` + "`agent-insights eval freeze`" + `; layout and
invariants: docs/superpowers/specs/2026-07-03-insights-outcome-eval-design.md
in the tmux-ctrl repo. All fixtures are append-only.
`

// EnsureRepoScaffold writes README.md and .gitignore on first run only.
func EnsureRepoScaffold(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	readme := filepath.Join(dataDir, "README.md")
	if _, err := os.Stat(readme); os.IsNotExist(err) {
		if err := os.WriteFile(readme, []byte(scaffoldReadme), 0o644); err != nil {
			return err
		}
	}
	gi := filepath.Join(dataDir, ".gitignore")
	if _, err := os.Stat(gi); os.IsNotExist(err) {
		if err := os.WriteFile(gi, []byte(".DS_Store\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}
