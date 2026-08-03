package transcript

import (
	"strings"
	"testing"
)

func TestDecodeBasicUserAndAssistant(t *testing.T) {
	in := `{"type":"user","cwd":"/repo","gitBranch":"main","version":"2.1.195","timestamp":"2026-06-29T10:00:00Z","message":{"role":"user","content":"hello"}}
{"type":"assistant","version":"2.1.195","message":{"id":"msg_1","role":"assistant","model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":2,"cache_read_input_tokens":100},"content":[{"type":"text","text":"hi"},{"type":"tool_use","name":"Read","input":{"file_path":"/a.go"}}]}}`
	ev, _ := DecodeTranscript(strings.NewReader(in))
	if len(ev) != 2 {
		t.Fatalf("events = %d, want 2", len(ev))
	}
	if ev[0].Cwd != "/repo" || ev[0].Message.Content[0].Type != "text" || ev[0].Message.Content[0].Text != "hello" {
		t.Fatalf("bare-string content not normalised: %+v", ev[0].Message.Content)
	}
	a := ev[1].Message
	if a.ID != "msg_1" || a.Usage.CacheRead != 100 || a.Content[1].ToolName != "Read" {
		t.Fatalf("assistant decode wrong: %+v", a)
	}
	if a.Content[1].ToolInput["file_path"] != "/a.go" {
		t.Fatalf("tool input wrong: %+v", a.Content[1].ToolInput)
	}
}

func TestDecodeSkipsMalformedAndTruncated(t *testing.T) {
	in := "{\"type\":\"user\",\"message\":{\"content\":\"a\"}}\nnot json\n{\"type\":\"assistant\",\"message\":{\"id\":\"m\",\"content\":[]}}\n{\"type\":\"user\",\"message\":{\"content\":\"trunc\"" // no closing
	ev, c := DecodeTranscript(strings.NewReader(in))
	if len(ev) != 2 {
		t.Fatalf("events = %d, want 2 (malformed + truncated skipped)", len(ev))
	}
	if c.MalformedFields["line"] < 2 {
		t.Fatalf("malformed not counted: %+v", c.MalformedFields)
	}
}

func TestDecodeReadsHugeLine(t *testing.T) {
	big := strings.Repeat("x", 2_000_000)
	in := `{"type":"user","message":{"content":"` + big + `"}}`
	ev, c := DecodeTranscript(strings.NewReader(in))
	if len(ev) != 1 || len(ev[0].Message.Content[0].Text) != 2_000_000 {
		t.Fatalf("huge line dropped; got %d events", len(ev))
	}
	if c.OverlongLines != 0 {
		t.Fatalf("OverlongLines = %d, want 0 (bufio.Reader unbounded)", c.OverlongLines)
	}
}

func TestDecodeStructuredPatch(t *testing.T) {
	in := `{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]},"toolUseResult":{"filePath":"/a.go","type":"update","structuredPatch":[{"lines":["+added","-removed"," ctx"]}]}}`
	ev, _ := DecodeTranscript(strings.NewReader(in))
	tr := ev[0].ToolUseResult
	if tr == nil || tr.FilePath != "/a.go" || tr.Kind != "update" {
		t.Fatalf("patch decode wrong: %+v", tr)
	}
	if len(tr.StructuredPatch) != 1 || len(tr.StructuredPatch[0].Lines) != 3 {
		t.Fatalf("patch hunks wrong: %+v", tr.StructuredPatch)
	}
}

func TestDecodeStringToolUseResultIgnored(t *testing.T) {
	in := `{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]},"toolUseResult":"just a string"}`
	ev, _ := DecodeTranscript(strings.NewReader(in))
	if ev[0].ToolUseResult != nil {
		t.Fatalf("string toolUseResult should decode to nil, got %+v", ev[0].ToolUseResult)
	}
}

func TestCanaryUnknownTypes(t *testing.T) {
	in := `{"type":"user","timestamp":"2026-06-29T10:00:00Z","message":{"content":[{"type":"text","text":"x"},{"type":"weird_block"}]}}
{"type":"brand_new_line"}`
	_, c := DecodeTranscript(strings.NewReader(in))
	if c.UnknownLineTypes["brand_new_line"] != 1 || c.UnknownBlockTypes["weird_block"] != 1 {
		t.Fatalf("unknown not flagged: %+v / %+v", c.UnknownLineTypes, c.UnknownBlockTypes)
	}
}

func TestCanaryMissingUsageAndVersions(t *testing.T) {
	in := `{"type":"assistant","version":"2.1.195","timestamp":"2026-06-29T10:00:00Z","message":{"id":"m","role":"assistant","content":[{"type":"text","text":"x"}]}}
{"type":"assistant","version":"2.2.0","timestamp":"2026-06-29T10:01:00Z","message":{"id":"n","role":"assistant","usage":{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[]}}`
	_, c := DecodeTranscript(strings.NewReader(in))
	if c.MissingFields["assistant.usage"] != 1 {
		t.Fatalf("missing usage not flagged: %+v", c.MissingFields)
	}
	if len(c.VersionsSeen) != 2 || c.Version != "2.2.0" {
		t.Fatalf("versions wrong: seen=%v last=%q", c.VersionsSeen, c.Version)
	}
}

func TestCanaryMissingTokenKey(t *testing.T) {
	// usage present but output_tokens renamed/absent -> silently 0 without a canary
	in := `{"type":"assistant","timestamp":"2026-06-29T10:00:00Z","message":{"id":"m","role":"assistant","usage":{"input_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[]}}`
	_, c := DecodeTranscript(strings.NewReader(in))
	if c.MissingFields["assistant.usage.output_tokens"] != 1 {
		t.Fatalf("missing token key not flagged: %+v", c.MissingFields)
	}
}

func TestCanaryMalformedIsError(t *testing.T) {
	in := `{"type":"user","timestamp":"2026-06-29T10:00:00Z","message":{"content":[{"type":"tool_result","is_error":"yes","content":"x"}]}}`
	ev, c := DecodeTranscript(strings.NewReader(in))
	if len(ev) != 1 {
		t.Fatalf("line wrongly dropped on non-bool is_error; got %d events", len(ev))
	}
	if c.MalformedFields["is_error"] != 1 {
		t.Fatalf("non-bool is_error not flagged: %+v", c.MalformedFields)
	}
}

func TestCanaryCleanSessionSilent(t *testing.T) {
	in := `{"type":"user","timestamp":"2026-06-29T10:00:00Z","message":{"content":[{"type":"tool_result","content":"ok"}]}}`
	_, c := DecodeTranscript(strings.NewReader(in))
	if len(c.MalformedFields) != 0 || len(c.MissingFields) != 0 {
		t.Fatalf("clean session tripped canary: %+v / %+v", c.MalformedFields, c.MissingFields)
	}
}
