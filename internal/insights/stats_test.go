package insights

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"tmux-ctrl/internal/sources/claude"
)

func runExtract(t *testing.T, in string, repo RepoResolver) Result {
	t.Helper()
	ev, c := claude.DecodeTranscript(strings.NewReader(in))
	return Extract(ev, c, "s", repo)
}

func noRepo(string) string { return "" }

func TestExtractIdentity(t *testing.T) {
	in := `{"type":"user","cwd":"/repo/wt","gitBranch":"feat","version":"2.1.195","timestamp":"2026-06-29T10:00:00Z","message":{"content":"hi"}}
{"type":"ai-title","aiTitle":"Fix the thing"}
{"type":"assistant","version":"2.1.195","timestamp":"2026-06-29T10:05:00Z","message":{"id":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"done"}]}}`
	r := runExtract(t, in, func(cwd string) string { return "/repo" })
	s := r.Stats
	if s.SessionID != "s" || s.Repo != "/repo" || s.Cwd != "/repo/wt" || s.GitBranch != "feat" {
		t.Fatalf("identity wrong: %+v", s)
	}
	if s.AiTitle != "Fix the thing" || s.Version != "2.1.195" || s.WallClock != 5*time.Minute {
		t.Fatalf("title/version/wallclock wrong: title=%q version=%q wall=%v", s.AiTitle, s.Version, s.WallClock)
	}
}

func TestTokensAndTurnsDedupByMessageID(t *testing.T) {
	// one assistant MESSAGE split across 3 lines, identical usage on each
	line := `{"type":"assistant","message":{"id":"msg_A","role":"assistant","model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":5,"cache_read_input_tokens":100},"content":[{"type":"%s"}]}}`
	in := fmt.Sprintf(line, "thinking") + "\n" + fmt.Sprintf(line, "text") + "\n" + fmt.Sprintf(line, "tool_use")
	// a second distinct message with a higher cache_read
	in += "\n" + `{"type":"assistant","message":{"id":"msg_B","role":"assistant","model":"claude-opus-4-8","usage":{"input_tokens":3,"output_tokens":4,"cache_creation_input_tokens":1,"cache_read_input_tokens":250},"content":[{"type":"text","text":"y"}]}}`
	s := runExtract(t, in, noRepo).Stats
	if s.AssistantTurns != 2 {
		t.Fatalf("AssistantTurns = %d, want 2 (per message.id)", s.AssistantTurns)
	}
	if s.Tokens.Input != 13 || s.Tokens.Output != 24 || s.Tokens.CacheCreation != 6 {
		t.Fatalf("token sums double-counted: %+v", s.Tokens)
	}
	if s.Tokens.CacheReadPeak != 250 {
		t.Fatalf("CacheReadPeak = %d, want 250 (max not sum)", s.Tokens.CacheReadPeak)
	}
	if s.ModelMix["claude-opus-4-8"] != 2 {
		t.Fatalf("ModelMix = %v, want opus:2", s.ModelMix)
	}
}
