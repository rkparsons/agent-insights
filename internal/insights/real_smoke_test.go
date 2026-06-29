package insights

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tmux-ctrl/internal/sources/claude"
)

// TestRealSmoke runs the production pipeline over real transcripts when
// INSIGHTS_REAL_SESSIONS (a glob) is set. Real sessions are private and
// machine-specific, so nothing is committed; this is a manual gate.
//
//	INSIGHTS_REAL_SESSIONS="$HOME/.claude/projects/<proj>/*.jsonl" go test ./internal/insights/ -run TestRealSmoke -v
func TestRealSmoke(t *testing.T) {
	glob := os.Getenv("INSIGHTS_REAL_SESSIONS")
	if glob == "" {
		t.Skip("set INSIGHTS_REAL_SESSIONS=<glob> to run")
	}
	files, err := filepath.Glob(glob)
	if err != nil || len(files) == 0 {
		t.Fatalf("glob %q matched nothing: %v", glob, err)
	}
	for _, f := range files {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			ev, c := claude.DecodeTranscript(strings.NewReader(string(data)))
			if len(ev) == 0 {
				t.Fatal("decoded 0 events")
			}
			r := Extract(ev, c, strings.TrimSuffix(name, ".jsonl"), noRepo)
			s := r.Stats

			// Reduced input is bounded (spine ~exempt but observed max ~83K).
			if r.Reduced.Chars > 200_000 {
				t.Errorf("reduced too large: %d chars", r.Reduced.Chars)
			}
			if !strings.HasPrefix(r.Reduced.Text, "Session: ") {
				t.Error("reduced missing header")
			}
			if r.Reduced.KeptEvents == 0 {
				t.Error("no events kept")
			}
			// Spine preservation: every real user turn is in the reduced output.
			if got := strings.Count(r.Reduced.Text, "\n[User]: "); got < s.UserTurns {
				// header line "User turns:" is not "[User]: " so no off-by-one
				t.Errorf("reduced [User] lines = %d < UserTurns stat = %d (spine not preserved)", got, s.UserTurns)
			}
			// Canary must be clean on the current format (the whole point of the gate).
			if len(c.UnknownLineTypes) != 0 {
				t.Errorf("unknown line types (parser needs updating): %v", c.UnknownLineTypes)
			}
			if len(c.UnknownBlockTypes) != 0 {
				t.Errorf("unknown block types: %v", c.UnknownBlockTypes)
			}
			if n := c.MalformedFields["line"]; n > 1 {
				t.Errorf("%d malformed lines (>1 truncated-tail tolerance)", n)
			}

			t.Logf("events=%d asstTurns=%d userTurns=%d toolErr=%d interrupts=%d rejections=%d taskNotif=%d subagents=%v skills=%v reducedChars=%d cacheReadPeak=%d versions=%v",
				len(ev), s.AssistantTurns, s.UserTurns, s.ToolErrors, s.Interrupts, s.Rejections,
				s.TaskNotifications, s.Subagents, s.Skills, r.Reduced.Chars, s.Tokens.CacheReadPeak, c.VersionsSeen)
		})
	}
}
