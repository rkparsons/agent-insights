package synthesis

// Recovery seam: feeds a captured synthesis.json — the model output a global
// run wrote, from a live workdir or from the diagnostics copy a failed run
// preserved — through verification and the store, at zero re-spend. For runs
// where claude finished but the Go side failed (a verifier fix, a crash, a
// timeout after the file landed).

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

func TestDevStoreCapturedSynthesis(t *testing.T) {
	path := os.Getenv("CAPTURED_SYNTH")
	if path == "" {
		t.Skip("set CAPTURED_SYNTH=<synthesis.json> to re-verify and store a captured global run")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw insights.RawGlobalSynthesis
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("captured output unusable: %v", err)
	}
	cfg, err := insights.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	analyses, err := LoadAnalyses()
	if err != nil {
		t.Fatal(err)
	}
	// The bundles must be the ones the capture was produced from: same store,
	// same floor, same aliases. A changed pool re-verifies against evidence the
	// model never saw and fails closed on ids it legitimately cited.
	bundles := buildBundles(GroupByRepo(analyses, cfg.MinSessions, cfg))
	snapshot, err := VerifyGlobal(context.Background(), raw, bundles, cfg, time.Now().UTC())
	if err != nil {
		t.Fatalf("verification: %v", err)
	}
	stored, err := StoreGlobal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("stored captured synthesis: %s (%d findings, %d notes)", stored, len(snapshot.Findings), len(snapshot.Meta.ValidationNotes))
}
