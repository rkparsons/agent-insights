package synthesis

// Bundle dump: builds the production bundles exactly as RunSynthesize would
// and writes them to BUNDLE_DUMP_DIR with size stats — the free probe for
// output-cap risk (a large-repo run has brushed the 64k output-token cap).
// Read-only over the live store.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rkparsons/agent-insights/internal/insights"
)

func TestDevBundleDump(t *testing.T) {
	outDir := os.Getenv("BUNDLE_DUMP_DIR")
	if outDir == "" {
		t.Skip("set BUNDLE_DUMP_DIR to dump production bundles")
	}
	analyses, err := LoadAnalyses()
	if err != nil {
		t.Fatal(err)
	}
	// The loaded config, not a zero Config: aliases and min_sessions decide
	// which buckets exist, so an empty one dumps bundles no run would build.
	cfg, err := insights.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	groups := GroupByRepo(analyses, cfg.MinSessions, cfg)
	for k, g := range groups {
		b := BuildBundle(k, g)
		raw, err := json.MarshalIndent(b, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(outDir, k+"-bundle.json")
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d analyses → bundle %.1f KB · friction %d · prefs %d · success %d · signals %d",
			k, len(g), float64(len(raw))/1024, len(b.Friction), len(b.Prefs), len(b.Success), len(b.Signals))
	}
}
