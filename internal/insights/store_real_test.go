package insights

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestRunSingleReal exercises the full wiring (real claude -p) on one session when
// INSIGHTS_REAL_SESSION (an id or path) is set. Manual gate — real subscription call.
//
//	INSIGHTS_REAL_SESSION="<session-id-or-path>" \
//	  go test ./internal/insights/ -run TestRunSingleReal -v -timeout 15m
func TestRunSingleReal(t *testing.T) {
	target := os.Getenv("INSIGHTS_REAL_SESSION")
	if target == "" {
		t.Skip("set INSIGHTS_REAL_SESSION=<id|path> to run")
	}
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	sum, err := RunSingle(context.Background(), target, cfg.Resolver(), NewClaudeJudge, Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: 10 * time.Minute})
	if err != nil {
		t.Fatalf("RunSingle: %v", err)
	}
	if sum.Analyzed != 1 {
		t.Fatalf("want Analyzed=1, got %+v", sum)
	}
	id := sessionIDFromPath(target)
	if mt, ok := ReadAnalysisMtime(id); !ok || mt.IsZero() {
		t.Errorf("stamped mtime missing for %q", id)
	}
}
