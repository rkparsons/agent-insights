package synthesis

// Recovery seam: feeds a captured claude synthesis envelope through the full
// RunSynthesize pipeline — verification, privacy scan, store — via an
// injected Synthesizer, at zero re-spend. For runs where claude finished but
// the Go side died (timeout, 429 park, crash) and the envelope was captured.

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/rkparsons/agent-insights/internal/insights"
)

type capturedSynthesizer struct{ raw RawSynthesis }

func (c capturedSynthesizer) Synthesize(ctx context.Context, b EvidenceBundle) (RawSynthesis, error) {
	return c.raw, nil
}

func TestDevStoreCapturedSynthesis(t *testing.T) {
	path := os.Getenv("CAPTURED_SYNTH")
	repo := os.Getenv("CAPTURED_REPO")
	if path == "" || repo == "" {
		t.Skip("set CAPTURED_SYNTH=<envelope json> and CAPTURED_REPO=<repo key>")
	}
	rawFile, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := insights.ParseClaudeEnvelope(rawFile)
	if err != nil {
		t.Fatalf("captured envelope unusable: %v", err)
	}
	var raw RawSynthesis
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatal(err)
	}
	sum, err := RunSynthesize(context.Background(), fixedSynth(capturedSynthesizer{raw: raw}), Options{Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Written != 1 || sum.Skipped != 0 {
		t.Fatalf("want Written=1 Skipped=0 for %s, got %+v", repo, sum)
	}
	t.Logf("stored captured synthesis for %s", repo)
}
