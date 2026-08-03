package insights

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSynthesizeLogPath guards the shared path shape: <InsightsDir>/logs/synthesize-<UTC date>.log.
// cmd/insights.go's `status --json` and internal/app/actions.go's synthesis-
// window spawn both call this instead of deriving the path themselves.
func TestSynthesizeLogPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", dir)
	want := filepath.Join(dir, "logs", "synthesize-"+time.Now().UTC().Format("2006-01-02")+".log")
	if got := SynthesizeLogPath(); got != want {
		t.Errorf("SynthesizeLogPath() = %q, want %q", got, want)
	}
}

func TestWriteAndReadAnalysisMtime(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", dir)
	mt := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	a := AgentSessionAnalysis{
		Stats:           AgentSessionStats{SessionID: "sess-1"},
		TranscriptMtime: mt,
	}
	if err := WriteAnalysis(a); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "analyses", "sess-1.json")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("analysis not written: %v", err)
	}
	got, ok := ReadAnalysisMtime("sess-1")
	if !ok || !got.Equal(mt) {
		t.Errorf("ReadAnalysisMtime = %v, %v; want %v, true", got, ok, mt)
	}
}

func TestReadAnalysisMtimeMissing(t *testing.T) {
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	if _, ok := ReadAnalysisMtime("nope"); ok {
		t.Error("want ok=false for missing analysis")
	}
}

func TestAtomicWriteNoPartialFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	if err := atomicWriteFile(p, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("want exactly the final file, got %d entries (temp leaked?)", len(entries))
	}
}
