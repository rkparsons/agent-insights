package insightseval

import (
	"os"
	"path/filepath"
	"testing"

	"tmux-ctrl/internal/synthesis"
)

// TestGroupingMatchesFrozenBenchmark verifies synthesis.RepoKey still maps
// every benchmark session (via its pool-v1 analysis) into its recorded bucket —
// the grouping property the explicit id lists otherwise take off the eval
// surface. Skips when the private data repo is absent.
func TestGroupingMatchesFrozenBenchmark(t *testing.T) {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, "Developer", "insights-eval-data")
	if _, err := os.Stat(filepath.Join(dataDir, "manifest.json")); err != nil {
		t.Skip("insights-eval-data not present")
	}
	b, ok, err := loadBenchmark(dataDir)
	if err != nil || !ok {
		t.Fatalf("benchmark: ok=%v err=%v", ok, err)
	}
	pool := filepath.Join(dataDir, "baseline-pool", "v1")
	// The frozen benchmark predates pipeline-owned config; terminal-app->tmux-ctrl was
	// the one hardcoded fold in place when it was captured.
	aliases := map[string]string{"terminal-app": "tmux-ctrl"}
	for bucket, bp := range b.Buckets {
		for _, id := range bp.AsConsumed {
			a, err := loadPoolAnalysis(filepath.Join(pool, id+".json"))
			if err != nil {
				t.Fatalf("%s/%s: %v", bucket, id, err)
			}
			if got := synthesis.RepoKey(a, aliases); got != bucket {
				t.Errorf("%s: RepoKey = %q, want %q", id, got, bucket)
			}
		}
	}
}
