package synthesis

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type errSynthesizer struct{}

func (errSynthesizer) Synthesize(ctx context.Context, b EvidenceBundle) (RawSynthesis, error) {
	return RawSynthesis{}, errors.New("boom")
}

func TestRunStateLifecycle(t *testing.T) {
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	if _, ok := ReadRunState(); ok {
		t.Fatal("no file: want ok=false")
	}
	// Empty store: RunSynthesize finds no groups, spends nothing, and must
	// still record a final ok state. A nil Synthesizer proves no call happens.
	if _, err := RunSynthesize(context.Background(), nil, Options{LogPath: "/tmp/x.log"}); err != nil {
		t.Fatal(err)
	}
	rs, ok := ReadRunState()
	if !ok || rs.Status != "ok" || rs.Written != 0 || rs.LogPath != "/tmp/x.log" {
		t.Fatalf("got %+v ok=%v", rs, ok)
	}
	if rs.PID != os.Getpid() || rs.StartedAt.IsZero() || rs.FinishedAt.IsZero() {
		t.Fatalf("identity fields: %+v", rs)
	}
}

func TestRunStateFailed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", dir)
	adir := filepath.Join(dir, "analyses")
	if err := os.MkdirAll(adir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnalysisFixture(t, adir, "s1", "repo1")
	_, err := RunSynthesize(context.Background(), errSynthesizer{}, Options{MinSessions: 1})
	if err != nil {
		t.Fatal(err) // per-repo failures skip, they don't error the run
	}
	rs, ok := ReadRunState()
	if !ok || rs.Status != "failed" || rs.Skipped != 1 || rs.Reason == "" {
		t.Fatalf("got %+v ok=%v", rs, ok)
	}
}

func TestRunStateDryRunWritesNothing(t *testing.T) {
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	if _, err := RunSynthesize(context.Background(), nil, Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadRunState(); ok {
		t.Fatal("dry-run must not write run state")
	}
}
