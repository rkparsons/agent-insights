package synthesis

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadLatestGlobalNewestWins(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	older := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	if _, err := StoreGlobal(globalFixture(newer, "new")); err != nil {
		t.Fatal(err)
	}
	if _, err := StoreGlobal(globalFixture(older, "old")); err != nil {
		t.Fatal(err)
	}

	got, ok, err := LoadLatestGlobal()
	if err != nil || !ok {
		t.Fatalf("LoadLatestGlobal = (ok=%v, err=%v)", ok, err)
	}
	if got.Meta.Model != "new" {
		t.Errorf("model = %q, want the newest snapshot's (write order must not matter)", got.Meta.Model)
	}
}

// TestLoadLatestGlobalSkipsMalformed keeps a truncated newest file from
// blanking the whole section: the newest PARSEABLE snapshot wins.
func TestLoadLatestGlobalSkipsMalformed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_INSIGHTS_DIR", dir)
	if _, err := StoreGlobal(globalFixture(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), "fallback")); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "synthesis", "global", "2026-08-13T00-00-00Z.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok, err := LoadLatestGlobal()
	if err != nil || !ok {
		t.Fatalf("LoadLatestGlobal = (ok=%v, err=%v)", ok, err)
	}
	if got.Meta.Model != "fallback" {
		t.Errorf("model = %q, want the newest parseable snapshot", got.Meta.Model)
	}
}

func TestLoadLatestGlobalNeverRun(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	got, ok, err := LoadLatestGlobal()
	if err != nil {
		t.Fatalf("err = %v, want nil for a store with no global dir", err)
	}
	if ok {
		t.Errorf("ok = true with no snapshot on disk: %+v", got)
	}
}

// TestLoadLatestGlobalUnreadableDir: anything other than "not there yet" is an
// error, not an empty section. A single global snapshot has no sibling to fall
// back to, so a silently-empty read would look exactly like "never run".
func TestLoadLatestGlobalUnreadableDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_INSIGHTS_DIR", dir)
	if err := os.MkdirAll(filepath.Join(dir, "synthesis"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A regular file where the global dir belongs: os.ReadDir returns ENOTDIR.
	if err := os.WriteFile(filepath.Join(dir, "synthesis", "global"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := LoadLatestGlobal(); err == nil || ok {
		t.Errorf("got (ok=%v, err=%v), want an error", ok, err)
	}
}
