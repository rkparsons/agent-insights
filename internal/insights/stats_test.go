package insights

import (
	"fmt"
	"reflect"
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

// markerAnchorFixture mixes GENUINE friction (markers at the start of their body —
// must stay counted) with MID-BODY quotes of the same markers (a transcript-inspecting
// / meta turn that merely mentions them — must NOT be counted). This is the H3
// regression: bare strings.Contains fabricates friction from the quotes.
const markerAnchorFixture = `{"type":"user","timestamp":"2026-06-29T10:00:00Z","message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"The user doesn't want to proceed with this tool use. The tool use was rejected. To tell you how to proceed, the user said: use the helper"}]}}
{"type":"user","message":{"content":"I think isInterruptText is wrong: it flags [Request interrupted by user] mid-body too"}}
{"type":"user","message":{"content":"Explain why a body like The user doesn't want to proceed with this tool use trips rejection detection"}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"9: interruptMarker = [Request interrupted by user (a constant in friction.go)"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"panic: parse error; note The user doesn't want to proceed with this tool use appeared in the log"}]}}`

func TestFrictionMarkerAnchoring(t *testing.T) {
	s := runExtract(t, markerAnchorFixture, noRepo).Stats
	if s.Interrupts != 1 {
		t.Errorf("Interrupts = %d, want 1 (only the prefix-0 text block; mid-body quotes must not count)", s.Interrupts)
	}
	if s.Rejections != 1 {
		t.Errorf("Rejections = %d, want 1 (only the prefix-0 is_error tool_result)", s.Rejections)
	}
	if s.ToolErrors != 1 {
		t.Errorf("ToolErrors = %d, want 1 (the is_error body that merely mentions the rejection marker)", s.ToolErrors)
	}
	if s.UserTurns != 2 {
		t.Errorf("UserTurns = %d, want 2 (the two marker-quoting prose turns are kept)", s.UserTurns)
	}
	if len(s.UserTurnFingerprints) != 2 {
		t.Errorf("fingerprints = %d, want 2 (kept turns are fingerprinted): %v", len(s.UserTurnFingerprints), s.UserTurnFingerprints)
	}
}

// The tool_result-body call site passes an UNTRIMMED body, so the anchor must
// tolerate leading whitespace before a genuine marker (the text-path body is
// already trimmed). No such case exists in today's corpus, but the classifier
// must not depend on that accident.
func TestFrictionMarkerToleratesLeadingWhitespace(t *testing.T) {
	in := `{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"\n  The user doesn't want to proceed with this tool use. The tool use was rejected. STOP what you are doing and wait for the user to tell you how to proceed."}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"  [Request interrupted by user]"}]}}`
	s := runExtract(t, in, noRepo).Stats
	if s.Rejections != 1 {
		t.Errorf("Rejections = %d, want 1 (whitespace-led genuine rejection body)", s.Rejections)
	}
	if s.Interrupts != 1 {
		t.Errorf("Interrupts = %d, want 1 (whitespace-led genuine interrupt body)", s.Interrupts)
	}
}

// enforceMarkerFixture mixes a GENUINE user turn, the injected
// [structured-output-enforce] marker (a synthetic pseudo-user turn the harness
// injects when the model skips the StructuredOutput tool — must be dropped, it is
// not what the user said), and a turn that merely MENTIONS the marker mid-body (a
// transcript-inspecting/meta turn — must be KEPT). Corpus-validated: the marker
// only ever appears prefix-0 in meta projects (37 turns, 0 non-meta); no genuine
// user turn begins with it.
const enforceMarkerFixture = `{"type":"user","message":{"content":"analyze this agent session please"}}
{"type":"user","message":{"content":[{"type":"text","text":"[structured-output-enforce] You MUST call the StructuredOutput tool to complete this request. Call this tool now."}]}}
{"type":"user","message":{"content":"Why does [structured-output-enforce] leak into the corpus as fabricated friction?"}}`

func TestStructuredOutputEnforceStripped(t *testing.T) {
	s := runExtract(t, enforceMarkerFixture, noRepo).Stats
	if s.UserTurns != 2 {
		t.Errorf("UserTurns = %d, want 2 (genuine turn + mid-body mention kept; injected [structured-output-enforce] dropped)", s.UserTurns)
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

func TestMechanicalFrictionClassification(t *testing.T) {
	rejNoReason := `The user doesn't want to proceed with this tool use. The tool use was rejected. STOP what you are doing and wait for the user to tell you how to proceed.`
	rejReasoned := `The user doesn't want to proceed with this tool use. To tell you how to proceed, the user said: use rtk proxy instead`
	in := `{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"<tool_use_error>File has not been read yet. Read it first before writing to it.</tool_use_error>"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"<tool_use_error>File has not been read yet. Read it first before writing to it.</tool_use_error>"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"Exit code 1\n(eval):cd:1: no such file or directory: src"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"<tool_use_error>String to replace not found in file.</tool_use_error>"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"Exit code 1\nmake: *** Error 2"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"` + rejNoReason + `"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"` + rejReasoned + `"}]}}`
	s := runExtract(t, in, noRepo).Stats
	if s.MechanicalFriction[modeEditBeforeRead] != 2 || s.MechanicalFriction[modeWrongCwd] != 1 ||
		s.MechanicalFriction[modePermission] != 1 || s.MechanicalFriction[modeOther] != 2 {
		t.Errorf("MechanicalFriction = %v", s.MechanicalFriction)
	}
	if s.Rejections != 2 {
		t.Errorf("Rejections = %d, want 2 (classification must not change the rejection split)", s.Rejections)
	}
	if s.ToolErrors != 5 {
		t.Errorf("ToolErrors = %d, want 5", s.ToolErrors)
	}
	if got := s.MechanicalExemplars[modeEditBeforeRead]; got != "File has not been read yet. Read it first before writing to it." {
		t.Errorf("exemplar = %q", got)
	}
	if _, ok := s.MechanicalExemplars[modePermission]; ok {
		t.Error("permission mode must carry no exemplar (boilerplate)")
	}
	if s.OtherErrorSignatures["String to replace not found in file."] != 1 ||
		s.OtherErrorSignatures["Exit code N"] != 1 {
		t.Errorf("OtherErrorSignatures = %v", s.OtherErrorSignatures)
	}
}

func TestDirectiveClausesAggregation(t *testing.T) {
	in := `{"type":"user","message":{"content":"please assign an opus subagent to do a critical review"}}
{"type":"user","message":{"content":"please assign an opus subagent to do a critical review"}}
{"type":"user","message":{"content":"now leave the branch as-is for manual testing"}}`
	s := runExtract(t, in, noRepo).Stats
	want := []DirectiveClause{
		{Norm: "now leave the branch as-is for manual testing",
			Exemplar: "now leave the branch as-is for manual testing", Count: 1, FirstTurn: 0},
		{Norm: "please assign an opus subagent to do a critical review",
			Exemplar: "please assign an opus subagent to do a critical review", Count: 2, FirstTurn: 1},
	}
	if !reflect.DeepEqual(s.DirectiveClauses, want) {
		t.Errorf("DirectiveClauses = %#v\nwant %#v", s.DirectiveClauses, want)
	}
}
