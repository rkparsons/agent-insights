package insights

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPipelineImportBoundary(t *testing.T) {
	// Find module root via go env GOMOD
	gomod := exec.Command("go", "env", "GOMOD")
	modPath, err := gomod.Output()
	if err != nil {
		t.Fatalf("failed to find go.mod: %v", err)
	}
	moduleRoot := filepath.Dir(strings.TrimSpace(string(modPath)))

	cmd := exec.Command("go", "list", "-deps", "-test",
		"./internal/insights/...", "./internal/synthesis/...",
		"./internal/eval/...", "./internal/transcript/...", "./skills/...")
	cmd.Dir = moduleRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	// agent-insights is a standalone extraction with no TUI side; none of the
	// tmux-ctrl-era app/userconfig/sources/tmux/agent/worktree/dashboard
	// packages exist here. bubbletea is the live risk (third_party/bubbletea's
	// local replace is gone — go mod tidy must not have pulled the real
	// module back in via a stray import). The rest stay as a zero-cost guard
	// against ever growing a TUI side back onto this pipeline.
	for _, banned := range []string{"bubbletea", "github.com/rkparsons/agent-insights/internal/app",
		"github.com/rkparsons/agent-insights/internal/userconfig", "github.com/rkparsons/agent-insights/internal/sources",
		"github.com/rkparsons/agent-insights/internal/tmux", "github.com/rkparsons/agent-insights/internal/agent",
		"github.com/rkparsons/agent-insights/internal/worktree", "github.com/rkparsons/agent-insights/internal/dashboard"} {
		if strings.Contains(string(out), banned) {
			t.Errorf("pipeline depends on %s", banned)
		}
	}
}
