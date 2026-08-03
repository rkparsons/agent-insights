package insights

import (
	"testing"
	"time"
)

func TestManifestLastEntryWins(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	t0 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	if err := appendManifest(ManifestEntry{SessionID: "s", TranscriptMtime: t0, Outcome: "gated", Threshold: 5, At: t0}); err != nil {
		t.Fatal(err)
	}
	if err := appendManifest(ManifestEntry{SessionID: "s", TranscriptMtime: t1, Outcome: "errored", Error: "boom", At: t1}); err != nil {
		t.Fatal(err)
	}
	m, err := loadManifest()
	if err != nil {
		t.Fatal(err)
	}
	e, ok := m["s"]
	if !ok || e.Outcome != "errored" || !e.TranscriptMtime.Equal(t1) {
		t.Errorf("last entry should win: %+v ok=%v", e, ok)
	}
}

func TestLoadManifestMissingIsEmpty(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	m, err := loadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Errorf("want empty map, got %d", len(m))
	}
}
