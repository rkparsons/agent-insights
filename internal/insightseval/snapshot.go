package insightseval

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tmux-ctrl/internal/insights"
	"tmux-ctrl/internal/synthesis"
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

// copyTree copies every file under srcRoot accepted by keep into dstRoot,
// preserving relative paths. A missing srcRoot copies nothing.
func copyTree(srcRoot, dstRoot string, keep func(rel string) bool) (int, error) {
	n := 0
	err := filepath.WalkDir(srcRoot, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(srcRoot, p)
		if relErr != nil || !keep(rel) {
			return nil
		}
		if err := copyFileRaw(p, filepath.Join(dstRoot, rel)); err != nil {
			return err
		}
		n++
		return nil
	})
	if os.IsNotExist(err) {
		return n, nil
	}
	return n, err
}

// CopyGroundTruth freezes the live synthesis artifacts (the JSONs whose
// Theme.SessionIDs are the only valid anchor source) into ground-truth/.
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

// SnapshotConfig freezes the config surface the adopted-check greps and the
// env-pinning later composes from: the global ~/.claude surface (top-level
// *.md, settings.json, full skills tree) plus each bucket repo's CLAUDE.md and
// .claude tree.
func SnapshotConfig(dataDir string, buckets map[string]BucketPopulations) (int, error) {
	home, _ := os.UserHomeDir()
	total := 0
	globalDst := filepath.Join(dataDir, "config-snapshot", "global")
	n, err := copyTree(filepath.Join(home, ".claude"), globalDst, func(rel string) bool {
		if strings.HasPrefix(rel, "skills"+string(filepath.Separator)) {
			return true
		}
		if !strings.Contains(rel, string(filepath.Separator)) {
			return strings.HasSuffix(rel, ".md") || rel == "settings.json"
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

PRIVATE fixture data for the tmux-ctrl insights outcome-eval suite. Contains
raw Claude Code session transcripts (including work content) — never make this
repo public and never copy its contents into a shareable artifact.

Produced and verified by ` + "`tmux-ctrl insights eval freeze`" + `; layout and
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
