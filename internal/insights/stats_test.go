package insights

import (
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
