package synthesis

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunStateLifecycle(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	t.Setenv("AGENT_INSIGHTS_CONFIG", "/nonexistent/agent-insights.yaml")
	if _, ok := ReadRunState(); ok {
		t.Fatal("no file: want ok=false")
	}
	// Empty store: RunSynthesize finds nothing to bundle, spends nothing, and
	// must still record a final ok state. A nil factory proves no call happens.
	if _, err := RunSynthesize(context.Background(), nil, Options{LogPath: "/tmp/x.log"}); err != nil {
		t.Fatal(err)
	}
	rs, ok := ReadRunState()
	if !ok || rs.Status != "ok" || rs.Written != 0 || rs.LogPath != "/tmp/x.log" {
		t.Fatalf("got %+v ok=%v", rs, ok)
	}
	if rs.PID != os.Getpid() || rs.StartedAt.IsZero() || rs.FinishedAt == nil || rs.FinishedAt.IsZero() {
		t.Fatalf("identity fields: %+v", rs)
	}
}

// TestRunStateRunningOmitsFinishedAt guards the bug where a plain (non-pointer)
// time.Time made `omitempty` a no-op: the "running" record written before a
// run completes must not carry a zero-value finished_at.
func TestRunStateRunningOmitsFinishedAt(t *testing.T) {
	rs := RunState{Status: "running", PID: os.Getpid(), StartedAt: time.Now().UTC()}
	data, err := json.Marshal(rs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "finished_at") {
		t.Fatalf("running record must omit finished_at, got %s", data)
	}
}

// TestRunStateMissingFactory: a run that reaches the model stage with no
// synthesizer factory is a wiring bug, and must be recorded as a failure
// rather than reported as a clean empty run.
func TestRunStateMissingFactory(t *testing.T) {
	seedStore(t, "alpha", 12)
	if _, err := RunSynthesize(context.Background(), nil, Options{MinSessions: 10}); err == nil {
		t.Fatal("expected an error with no synthesizer factory")
	}
	rs, ok := ReadRunState()
	if !ok || rs.Status != "failed" || rs.Reason == "" {
		t.Fatalf("got %+v ok=%v", rs, ok)
	}
}

func TestRunStateDryRunWritesNothing(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	t.Setenv("AGENT_INSIGHTS_CONFIG", "/nonexistent/agent-insights.yaml")
	if _, err := RunSynthesize(context.Background(), nil, Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadRunState(); ok {
		t.Fatal("dry-run must not write run state")
	}
}
