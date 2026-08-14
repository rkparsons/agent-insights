package synthesis

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

func synthesisDir() string { return filepath.Join(insights.InsightsDir(), "synthesis") }

// Dir returns the synthesis artifacts root (exported for the eval freeze).
func Dir() string { return synthesisDir() }

// globalDir holds the cross-repo snapshots, one per accepted run. The v1
// per-repo directories sit beside it, untouched and unread — nothing migrates
// them, and nothing loads them (spec §Verification).
func globalDir() string { return filepath.Join(synthesisDir(), "global") }

// diagnosticsDir holds the model output of runs that never produced a
// snapshot. See preserveFailedSynthesis.
func diagnosticsDir() string { return filepath.Join(synthesisDir(), "diagnostics") }

// snapshotTimeLayout names a snapshot file after its instant, colon-free so it
// is a legal filename on every platform, and fixed-width UTC so lexical order
// is chronological order — which is what makes "newest" a filename sort.
const snapshotTimeLayout = "2006-01-02T15-04-05Z"

// globalRetention is how many global snapshots (and failed-run diagnostics
// copies) the store keeps. Older ones are pruned on write.
const globalRetention = 10

// StoreGlobal writes one accepted global synthesis to
// synthesis/global/<generated_at>.json and prunes to the newest
// globalRetention snapshots. Returns the path written.
//
// Unlike v1's per-repo pair there is no rendered .md sibling: the snapshot is
// the artifact, and the verifier has already run the privacy scan over every
// free-text field it carries (verify2.go).
func StoreGlobal(s insights.GlobalSynthesisJSON) (string, error) {
	dir := globalDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, s.GeneratedAt.UTC().Format(snapshotTimeLayout)+".json")
	if err := atomicWrite(path, data); err != nil {
		return "", err
	}
	return path, pruneSnapshots(dir, globalRetention)
}

// preserveFailedSynthesis copies a failed run's model output out of the
// scratch workdir, which is removed when the run ends. A 90-minute run that
// fails verification leaves nothing else to post-mortem, so the copy happens
// before the error is returned. Returns "" when the run produced no output at
// all (the CLI died first) — not an error, just nothing to keep.
func preserveFailedSynthesis(workDir string, at time.Time) (string, error) {
	data, err := os.ReadFile(filepath.Join(workDir, globalOutputFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	dir := diagnosticsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, at.UTC().Format(snapshotTimeLayout)+".json")
	if err := atomicWrite(path, data); err != nil {
		return "", err
	}
	return path, pruneSnapshots(dir, globalRetention)
}

// pruneSnapshots deletes all but the newest keep .json files in dir, by the
// chronological-lexical filename order snapshotTimeLayout guarantees.
func pruneSnapshots(dir string, keep int) error {
	names, err := snapshotJSONNames(dir)
	if err != nil {
		return err
	}
	if len(names) <= keep {
		return nil
	}
	for _, name := range names[:len(names)-keep] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("prune %s: %w", name, err)
		}
	}
	return nil
}

func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
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
	return os.Rename(name, path)
}
