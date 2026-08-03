package insightseval

// Env-hash probe: reports which (claudeVersion, snapshot) tuple produces a
// verdict's matcher_env_hash. Run before any claude-version shim — a shim to
// the wrong tuple turns a cache-served re-score into a paid sweep. Guarded by
// ENV_PROBE.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDevEnvHashProbe(t *testing.T) {
	scratchData := os.Getenv("ENV_PROBE")
	if scratchData == "" {
		t.Skip("set ENV_PROBE=<scratch data dir> to run")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	realData := filepath.Join(home, "Developer", "insights-eval-data")
	for _, dir := range []string{realData, scratchData} {
		snap, err := hashTree(filepath.Join(dir, "config-snapshot", "global"))
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		for _, v := range []string{"2.1.204 (Claude Code)", "2.1.205 (Claude Code)", "2.1.206 (Claude Code)"} {
			env := cacheKey("env", v, snap)
			mark := ""
			if env == devCurEnvHash {
				mark = "  <== MATCHES verdict matcher_env_hash"
			}
			fmt.Printf("%s · %s · snap %.12s · env %.12s%s\n", filepath.Base(dir), v, snap, env, mark)
		}
	}
}
