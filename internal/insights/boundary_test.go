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

	cmd := exec.Command("go", "list", "-deps",
		"./internal/insights/...", "./internal/synthesis/...",
		"./internal/insightseval/...", "./internal/transcript/...", "./skills/...")
	cmd.Dir = moduleRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"bubbletea", "tmux-ctrl/internal/app",
		"tmux-ctrl/internal/userconfig", "tmux-ctrl/internal/sources",
		"tmux-ctrl/internal/tmux", "tmux-ctrl/internal/agent",
		"tmux-ctrl/internal/worktree", "tmux-ctrl/internal/dashboard"} {
		if strings.Contains(string(out), banned) {
			t.Errorf("pipeline depends on %s", banned)
		}
	}
}
