package synthesis

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdoptChecker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("Never add comments that restate what the code does."), 0o644); err != nil {
		t.Fatal(err)
	}
	check := newAdoptCheckerFromFiles([]string{filepath.Join(dir, "CLAUDE.md")})
	if got := check(Recommendation{Statement: "Avoid comments that restate what the code does"}); got != "yes" {
		t.Errorf("adopted rule → %q, want yes", got)
	}
	if got := check(Recommendation{Statement: "Prefer trunk-based development with short-lived branches"}); got != "no" {
		t.Errorf("unadopted rule → %q, want no", got)
	}
}
