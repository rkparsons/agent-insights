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

// fileAction is what a tree walk does with one accepted (live, frozen) pair.
// The freeze passes copyFileRaw; the pre-flight conflict check passes a
// comparator that writes nothing — the same walk either way, so the check can
// never disagree with what the freeze would actually do.
type fileAction func(src, dst string) error

// copyTreeVisited is the recursive implementation of copyTree with cycle detection.
func copyTreeVisited(srcRoot, dstRoot string, keep func(string) bool, apply fileAction, visited map[string]bool) (int, error) {
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
				}, apply, visited)
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
		if err := apply(p, filepath.Join(dstRoot, rel)); err != nil {
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
	return copyTreeWith(srcRoot, dstRoot, keep, copyFileRaw)
}

// copyTreeWith is copyTree with the per-file action injected (see fileAction).
func copyTreeWith(srcRoot, dstRoot string, keep func(rel string) bool, apply fileAction) (int, error) {
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
	return copyTreeVisited(resolved, dstRoot, keep, apply, visited)
}

// CopyGroundTruth freezes the live v1 per-repo synthesis reports into
// ground-truth/. The v2 global snapshots sitting beside them in the store are
// explicitly NOT part of this leg: they freeze through CopyGlobalGroundTruth
// into the subdir loadGlobalGroundTruth reads, which is what keeps them out of
// loadGroundTruth's per-repo walk and off the cards as a phantom repo bucket.
// Two legs rather than one wholesale copy because the two are canonical at
// different moments — see RunFreeze.
//
// Failed-run model output is NOT a concern here: it never passed the verifier's
// privacy scan and so lives outside Dir() entirely (synthesis.diagnosticsDir).
func CopyGroundTruth(dataDir string) (int, error) {
	globalPrefix := globalGroundTruthDir + string(filepath.Separator)
	return copyTree(synthesis.Dir(), filepath.Join(dataDir, "ground-truth"), func(rel string) bool {
		return !strings.HasPrefix(rel, globalPrefix)
	})
}

// CopyGlobalGroundTruth freezes the v2 cross-repo snapshots into
// ground-truth/global — the anchor source under the v2 contract, and the exact
// path loadGlobalGroundTruth reads.
func CopyGlobalGroundTruth(dataDir string) (int, error) {
	return copyTree(filepath.Join(synthesis.Dir(), globalGroundTruthDir),
		filepath.Join(dataDir, "ground-truth", globalGroundTruthDir), func(string) bool { return true })
}

// CopyBaselinePool freezes the analyses pool as baseline-pool/v1 — the judged
// fields that produced the ground truth. Call only when assertions are clean.
func CopyBaselinePool(dataDir string) (int, error) {
	return copyTree(insights.AnalysesDir(), filepath.Join(dataDir, "baseline-pool", "v1"), func(rel string) bool {
		return strings.HasSuffix(rel, ".json")
	})
}

// frozenEmptyRepoMarker records a repo root that resolved but held no CLAUDE.md
// and no .claude tree when the corpus was frozen. The directory has to exist in
// the frozen corpus — the manifest names it as a readable root, so the model
// asks production's asset-ladder question ("does this repo document this
// already?") and gets production's answer, which is different from the answer
// an unavailable root gives — and git does not track empty directories, so the
// directory needs a file to survive a commit. A dot-file keeps it out of the
// model's asset globs: an assetless repo must still look assetless.
const frozenEmptyRepoMarker = ".no-assets"

const frozenEmptyRepoBody = "This repo had no CLAUDE.md and no .claude/ when the eval corpus was frozen.\n"

// SnapshotConfig freezes the config surface the synthesis manifest points the
// model at and the env-pinning later composes from: the global ~/.claude surface (top-level
// *.md, settings.json, statusline.mjs, the full skills and hooks trees, and
// the plugin inventory — never the plugins cache) plus each bucket repo's
// CLAUDE.md and .claude tree.
func SnapshotConfig(dataDir string, buckets map[string]BucketPopulations, cfg insights.Config) (int, error) {
	return snapshotConfigWalk(dataDir, buckets, cfg, copyFileRaw, freezeEmptyRepoDir)
}

// ConfigSnapshotConflicts reports the frozen asset paths whose live counterpart
// now holds different bytes — the append-only violations SnapshotConfig would
// hit, found before anything is written. It walks exactly what the freeze walks
// (snapshotConfigWalk) and writes nothing.
func ConfigSnapshotConflicts(dataDir string, buckets map[string]BucketPopulations, cfg insights.Config) ([]string, error) {
	var conflicts []string
	_, err := snapshotConfigWalk(dataDir, buckets, cfg, func(src, dst string) error {
		live, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		frozen, err := os.ReadFile(dst)
		if errors.Is(err, os.ErrNotExist) {
			return nil // a new file is an append, not a conflict
		}
		if err != nil {
			return err
		}
		if !bytes.Equal(live, frozen) {
			conflicts = append(conflicts, dst)
		}
		return nil
	}, func(string) error { return nil })
	sort.Strings(conflicts)
	return conflicts, err
}

// snapshotConfigWalk is the one definition of what the config snapshot covers.
// visit receives every (live, frozen) file pair; emptyRepo receives the frozen
// directory of a repo root that resolved but carried no assets.
//
// A bucket's repo path is the configured checkout root when cfg lists one under
// the bucket's key, else the benchmark's RepoPath (an observed session cwd).
// The lookup maps each configured path's base name through cfg.Canonical, which
// is what makes it match a BUCKET key (bucket keys come from synthesis.RepoKey,
// which applies aliases). Production's synthesis.repoRootsFor does NOT do this
// — it pairs manifest roots on the plain base name — and the divergence is
// harmless here precisely because eval never hands it a live path: the roots it
// pairs are the frozen directories this function writes, whose base name IS the
// bucket key.
//
// A bucket whose path is empty or absent on disk freezes nothing at all: that
// root is unresolvable, and frozenAssetConfig warns about it and lets the
// manifest say "unavailable". A path that resolves but holds no assets is a
// different case — see emptyRepo below.
func snapshotConfigWalk(dataDir string, buckets map[string]BucketPopulations, cfg insights.Config,
	visit fileAction, emptyRepo func(dst string) error) (int, error) {
	home, _ := os.UserHomeDir()
	total := 0
	globalDst := filepath.Join(dataDir, "config-snapshot", "global")
	n, err := copyTreeWith(filepath.Join(home, ".claude"), globalDst, func(rel string) bool {
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
	}, visit)
	if err != nil {
		return total, err
	}
	total += n

	configured := make(map[string]string, len(cfg.Repos))
	for _, p := range cfg.Repos {
		configured[cfg.Canonical(filepath.Base(p))] = p
	}
	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		repoPath := configured[k]
		if repoPath == "" {
			repoPath = buckets[k].RepoPath
		}
		if repoPath == "" {
			continue
		}
		if info, err := os.Stat(repoPath); err != nil || !info.IsDir() {
			continue // unresolvable root: nothing to freeze, and nothing to claim
		}
		repoDst := filepath.Join(dataDir, "config-snapshot", "repos", k)
		assets := 0
		if _, err := os.Stat(filepath.Join(repoPath, "CLAUDE.md")); err == nil {
			if err := visit(filepath.Join(repoPath, "CLAUDE.md"), filepath.Join(repoDst, "CLAUDE.md")); err != nil {
				return total, err
			}
			assets++
		}
		n, err := copyTreeWith(filepath.Join(repoPath, ".claude"), filepath.Join(repoDst, ".claude"), func(rel string) bool {
			base := filepath.Base(rel)
			return strings.HasSuffix(rel, ".md") || base == "settings.json" || base == "settings.local.json"
		}, visit)
		if err != nil {
			return total, err
		}
		assets += n
		total += assets
		if assets == 0 {
			// Resolvable but assetless is a real answer, not a hole: the model
			// must be able to look and find nothing, exactly as it would in the
			// live repo.
			if err := emptyRepo(repoDst); err != nil {
				return total, err
			}
		}
	}
	return total, nil
}

// freezeEmptyRepoDir materializes the frozen directory of an assetless repo
// root. An already-frozen directory is left exactly as it is: the corpus is
// append-only, so a repo that has since deleted its CLAUDE.md keeps the copy the
// runs were scored against rather than gaining a marker that contradicts it.
func freezeEmptyRepoDir(dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, frozenEmptyRepoMarker), []byte(frozenEmptyRepoBody), 0o644)
}

// frozenAssetConfig builds the pipeline config an eval run hands the global
// synthesizer: repo roots redirected into the frozen corpus, the configured L2
// model, and no dotfiles_repo.
//
// The redirect has two halves. Repo roots are named here, keyed the way
// synthesis.repoRootsFor keys configured paths (by base name), so the manifest
// names <data>/config-snapshot/repos/<bucket> instead of a live checkout. The
// global half is the env pin's doing: the nested claude's CLAUDE_CONFIG_DIR is
// the ephemeral copy of config-snapshot/global, and
// NewClaudeGlobalSynthesizerPinned names that same dir as the manifest's
// globalRoot. Between them the model reads only frozen bytes.
//
// dotfiles_repo is omitted deliberately (spec §Eval adaptation): git history is
// the one input a freeze cannot pin, so eval takes the graceful-degradation
// path ("rule exists now") as its reproducibility answer. The dated escalation
// branch that skips is pure Go and is covered by the verifier's unit tests, so
// the gate loses nothing it could have measured.
//
// A bucket with no frozen repo config is omitted rather than pointed at a live
// path: the manifest then names it "unavailable", which is the honest state,
// and the omission is warned about so a hole in the corpus is never silent.
func frozenAssetConfig(dataDir string, buckets []string, synthesisModel string) (insights.Config, []string) {
	cfg := insights.Config{SynthesisModel: synthesisModel}
	var warnings []string
	for _, bucket := range buckets {
		root := filepath.Join(dataDir, "config-snapshot", "repos", bucket)
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			warnings = append(warnings, fmt.Sprintf(
				"%s: no frozen repo config under config-snapshot/repos — the synthesis manifest names its assets unavailable (re-run `agent-insights eval freeze`)", bucket))
			continue
		}
		cfg.Repos = append(cfg.Repos, root)
	}
	return cfg, warnings
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
