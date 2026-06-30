package insights

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"tmux-ctrl/internal/sources/claude"
)

func runExtract(t *testing.T, in string, repo RepoResolver) SessionExtraction {
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

func TestToolsAttributionEdits(t *testing.T) {
	in := `{"type":"assistant","attributionSkill":"superpowers:brainstorming","attributionPlugin":"superpowers","message":{"id":"m1","role":"assistant","usage":{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/a"}},{"type":"tool_use","name":"Agent","input":{"subagent_type":"Explore","description":"x"}}]}}
{"type":"assistant","message":{"id":"m2","role":"assistant","usage":{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/a.go"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]},"toolUseResult":{"filePath":"/a.go","type":"update","structuredPatch":[{"lines":["+x","+y","-z"]}]}}`
	s := runExtract(t, in, noRepo).Stats
	if s.ToolCounts["Read"] != 1 || s.ToolCounts["Agent"] != 1 || s.ToolCounts["Edit"] != 1 {
		t.Fatalf("tool counts: %v", s.ToolCounts)
	}
	if s.SubagentFanout != 1 || len(s.Subagents) != 1 || s.Subagents[0] != "Explore" {
		t.Fatalf("subagent: fan=%d names=%v", s.SubagentFanout, s.Subagents)
	}
	if len(s.Skills) != 1 || s.Skills[0] != "superpowers:brainstorming" || len(s.Plugins) != 1 || s.Plugins[0] != "superpowers" {
		t.Fatalf("attribution: %v / %v", s.Skills, s.Plugins)
	}
	if s.Edits != 1 || s.LinesAdded != 2 || s.LinesRemoved != 1 || len(s.FilesTouched) != 1 {
		t.Fatalf("edits=%d +%d -%d files=%v", s.Edits, s.LinesAdded, s.LinesRemoved, s.FilesTouched)
	}
}

func TestFrictionClassification(t *testing.T) {
	rej := `The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit). To tell you how to proceed, the user said: use the other file`
	rejNoReason := `The user doesn't want to proceed with this tool use. The tool use was rejected. STOP what you are doing and wait for the user to tell you how to proceed.`
	in := `{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"boom: file not found"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"` + rej + `"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"` + rejNoReason + `"}]}}
{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}
{"type":"user","message":{"content":"please do X"}}
{"type":"user","message":{"content":[{"type":"text","text":"<task-notification>\n<task-id>x</task-id>\nresult body"}]}}
{"type":"user","message":{"content":[{"type":"text","text":"<system-reminder>ignore me"}]}}`
	s := runExtract(t, in, noRepo).Stats
	if s.ToolErrors != 1 {
		t.Fatalf("ToolErrors = %d, want 1 (rejections excluded)", s.ToolErrors)
	}
	if s.Rejections != 2 {
		t.Fatalf("Rejections = %d, want 2", s.Rejections)
	}
	if s.Interrupts != 1 {
		t.Fatalf("Interrupts = %d, want 1", s.Interrupts)
	}
	if s.UserTurns != 1 {
		t.Fatalf("UserTurns = %d, want 1 (synthetic/task-notif/rejection/interrupt excluded)", s.UserTurns)
	}
	if s.TaskNotifications != 1 {
		t.Fatalf("TaskNotifications = %d, want 1", s.TaskNotifications)
	}
}

func TestUserTurnFingerprints(t *testing.T) {
	in := `{"type":"user","message":{"content":"Implement the parser for the config file please"}}
{"type":"user","message":{"content":"yes"}}
{"type":"user","message":{"content":"continue"}}
{"type":"user","message":{"content":"Now also handle the error case in the loader"}}`
	s := runExtract(t, in, noRepo).Stats
	if len(s.UserTurnFingerprints) != 2 {
		t.Fatalf("fingerprints = %d, want 2 (trivial 'yes'/'continue' skipped): %v", len(s.UserTurnFingerprints), s.UserTurnFingerprints)
	}
	if s.UserTurns != 4 {
		t.Fatalf("UserTurns = %d, want 4 (trivial turns still count as turns)", s.UserTurns)
	}
	s2 := runExtract(t, in, noRepo).Stats
	if s.UserTurnFingerprints[0] != s2.UserTurnFingerprints[0] {
		t.Fatalf("fingerprint not stable/content-derived across sessions")
	}
}
