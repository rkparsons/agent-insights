package synthesis

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", dir)
	s := sampleSynthesis()
	if err := Store(s, "# md", "2026-07-02"); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "synthesis", "alpha", "2026-07-02")
	if _, err := os.Stat(base + ".json"); err != nil {
		t.Errorf("json not written: %v", err)
	}
	if data, err := os.ReadFile(base + ".md"); err != nil || string(data) != "# md" {
		t.Errorf("md = %q err %v", data, err)
	}
}
