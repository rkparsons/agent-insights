package insights

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rkparsons/agent-insights/internal/transcript"
)

func runReducedBudget(t *testing.T, in string, repo RepoResolver, budget int) SessionExtraction {
	t.Helper()
	ev, c := transcript.DecodeTranscript(strings.NewReader(in))
	return extractWithBudget(ev, c, "s", repo, budget)
}

func TestReducerSpineAndLabels(t *testing.T) {
	in := `{"type":"user","cwd":"/r","gitBranch":"b","message":{"content":"do the thing"}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"boom"}]}}
{"type":"user","message":{"content":[{"type":"text","text":"<task-notification>\nresult"}]}}
{"type":"user","message":{"content":[{"type":"text","text":"The user doesn't want to proceed with this tool use. The tool use was rejected. To tell you how to proceed, the user said: use TDD"}]}}
{"type":"assistant","message":{"id":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"ok"},{"type":"tool_use","name":"Agent","input":{"subagent_type":"Explore","description":"look"}}]}}`
	out := runExtract(t, in, func(string) string { return "/r" }).Reduced.Text
	for _, want := range []string{"[User]: do the thing", "[ToolError]: boom", "[Subagent result]:", "[Rejected]: use TDD", "[Assistant]: ok", "[Tool: Agent Explore]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "doesn't want to proceed") {
		t.Fatalf("rejection preamble leaked into reduced output:\n%s", out)
	}
	if !strings.Contains(out, "Tool errors: 1") || !strings.Contains(out, "Rejections: 1") {
		t.Fatalf("header stats missing:\n%s", out)
	}
}

func TestReducerMarkerAnchoring(t *testing.T) {
	out := runExtract(t, markerAnchorFixture, noRepo).Reduced.Text
	for _, want := range []string{
		"[Interrupt]",
		"[Rejected]: use the helper",
		"[User]: I think isInterruptText",
		"[User]: Explain why a body",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "[Interrupt]"); n != 1 {
		t.Errorf("[Interrupt] rows = %d, want 1 (mid-body quotes must not render as friction):\n%s", n, out)
	}
	if n := strings.Count(out, "[Rejected]"); n != 1 {
		t.Errorf("[Rejected] rows = %d, want 1:\n%s", n, out)
	}
}

func TestReducerStripsStructuredOutputEnforce(t *testing.T) {
	out := runExtract(t, enforceMarkerFixture, noRepo).Reduced.Text
	if strings.Contains(out, "You MUST call the StructuredOutput tool") {
		t.Errorf("injected [structured-output-enforce] leaked into reduced output:\n%s", out)
	}
	for _, want := range []string{
		"[User]: analyze this agent session please",
		"[User]: Why does [structured-output-enforce] leak",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing genuine turn %q in:\n%s", want, out)
		}
	}
}

func TestReducerBudgetKeepsSpineDropsProse(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"type":"user","message":{"content":"KEEP USER"}}` + "\n")
	for i := 0; i < 50; i++ {
		b.WriteString(`{"type":"assistant","message":{"id":"m` + fmt.Sprint(i) + `","role":"assistant","usage":{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"` + strings.Repeat("z", 500) + `"}]}}` + "\n")
	}
	r := runReducedBudget(t, b.String(), noRepo, 2000)
	if !strings.Contains(r.Reduced.Text, "KEEP USER") {
		t.Fatal("spine user turn dropped under budget")
	}
	if r.Reduced.DroppedEvents == 0 {
		t.Fatalf("expected prose drops under tight budget; kept=%d dropped=%d", r.Reduced.KeptEvents, r.Reduced.DroppedEvents)
	}
}
