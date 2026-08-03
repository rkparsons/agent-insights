package transcript

import (
	"bufio"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"time"
)

// TranscriptEvent is one defensively-decoded transcript line. Every field is
// optional; absence is never an error. The Claude Code transcript format is
// officially internal/unstable, so decoding tolerates missing/renamed fields and
// records drift in the Canary rather than failing.
type TranscriptEvent struct {
	Type              string
	Timestamp         time.Time
	Cwd               string
	GitBranch         string
	Version           string
	UUID              string
	ParentUUID        string
	LeafUUID          string
	AiTitle           string
	AttributionSkill  string
	AttributionPlugin string
	Message           *Message
	ToolUseResult     *ToolResult
}

type Message struct {
	ID      string
	Role    string
	Model   string
	Usage   *Usage
	Content []ContentBlock
}

type ContentBlock struct {
	Type       string
	Text       string // text or thinking payload
	ToolName   string
	ToolInput  map[string]any
	HasIsError bool   // whether tool_result carried an is_error key
	IsError    bool   // its value (read only when HasIsError)
	ToolResult string // tool_result body, flattened to text
}

type ToolResult struct {
	FilePath        string
	Kind            string
	StructuredPatch []PatchHunk
}

type PatchHunk struct{ Lines []string }

type Usage struct{ Input, Output, CacheCreation, CacheRead int }

// Canary records format drift so a Claude Code update surfaces loudly instead of
// silently corrupting stats. A non-zero canary on a real run means "the format
// moved, fix the parser."
type Canary struct {
	UnknownLineTypes  map[string]int `json:"unknown_line_types"`
	UnknownBlockTypes map[string]int `json:"unknown_block_types"`
	MalformedFields   map[string]int `json:"malformed_fields"`
	MissingFields     map[string]int `json:"missing_fields"`
	OverlongLines     int            `json:"overlong_lines"`
	VersionsSeen      []string       `json:"versions_seen"`
	Samples           []string       `json:"samples"`
	Version           string         `json:"version"`
}

func newCanary() Canary {
	return Canary{
		UnknownLineTypes:  map[string]int{},
		UnknownBlockTypes: map[string]int{},
		MalformedFields:   map[string]int{},
		MissingFields:     map[string]int{},
	}
}

const maxCanarySamples = 10

func (c *Canary) addSample(line string) {
	if len(c.Samples) >= maxCanarySamples {
		return
	}
	if len(line) > 200 {
		line = line[:200]
	}
	c.Samples = append(c.Samples, line)
}

// DecodeTranscript reads a transcript JSONL stream into typed events plus a
// Canary. Lines are read unbounded (real lines reach ~1.8 MB); a malformed or
// truncated line is skipped and counted, never fatal.
func DecodeTranscript(r io.Reader) (events []TranscriptEvent, canary Canary) {
	canary = newCanary()
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			decodeLine(trimmed, &events, &canary)
		}
		if err != nil {
			break
		}
	}
	return events, canary
}

type rawLine struct {
	Type              string          `json:"type"`
	Timestamp         string          `json:"timestamp"`
	Cwd               string          `json:"cwd"`
	GitBranch         string          `json:"gitBranch"`
	Version           string          `json:"version"`
	UUID              string          `json:"uuid"`
	ParentUUID        string          `json:"parentUuid"`
	LeafUUID          string          `json:"leafUuid"`
	AiTitle           string          `json:"aiTitle"`
	AttributionSkill  string          `json:"attributionSkill"`
	AttributionPlugin string          `json:"attributionPlugin"`
	Message           *rawMessage     `json:"message"`
	ToolUseResult     json.RawMessage `json:"toolUseResult"`
}

type rawMessage struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Usage   *rawUsage       `json:"usage"`
	Content json.RawMessage `json:"content"`
}

type rawToolResult struct {
	FilePath        string         `json:"filePath"`
	Kind            string         `json:"type"`
	StructuredPatch []rawPatchHunk `json:"structuredPatch"`
}

type rawPatchHunk struct {
	Lines []string `json:"lines"`
}

type rawUsage struct {
	Input         *int `json:"input_tokens"`
	Output        *int `json:"output_tokens"`
	CacheCreation *int `json:"cache_creation_input_tokens"`
	CacheRead     *int `json:"cache_read_input_tokens"`
}

type rawBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Name     string          `json:"name"`
	Input    map[string]any  `json:"input"`
	IsError  json.RawMessage `json:"is_error"`
	Content  json.RawMessage `json:"content"`
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

var knownLineTypes = map[string]bool{
	"user": true, "assistant": true, "system": true, "summary": true,
	"ai-title": true, "last-prompt": true, "mode": true, "permission-mode": true,
	"file-history-snapshot": true, "queue-operation": true, "attachment": true,
}

var knownBlockTypes = map[string]bool{
	"text": true, "thinking": true, "tool_use": true, "tool_result": true, "image": true,
}

func decodeLine(line string, events *[]TranscriptEvent, canary *Canary) {
	var rl rawLine
	if err := json.Unmarshal([]byte(line), &rl); err != nil {
		canary.MalformedFields["line"]++
		canary.addSample(line)
		return
	}
	ev := TranscriptEvent{
		Type:              rl.Type,
		Cwd:               rl.Cwd,
		GitBranch:         rl.GitBranch,
		Version:           rl.Version,
		UUID:              rl.UUID,
		ParentUUID:        rl.ParentUUID,
		LeafUUID:          rl.LeafUUID,
		AiTitle:           rl.AiTitle,
		AttributionSkill:  rl.AttributionSkill,
		AttributionPlugin: rl.AttributionPlugin,
		Message:           rl.Message.toMessage(canary),
	}
	if rl.Timestamp != "" {
		if ts, e := time.Parse(time.RFC3339, rl.Timestamp); e == nil {
			ev.Timestamp = ts
		}
	}
	ev.ToolUseResult = decodeToolUseResult(rl.ToolUseResult)
	canaryCheck(&rl, ev, canary)
	*events = append(*events, ev)
}

// decodeToolUseResult extracts the edit/write fields. toolUseResult is often a
// non-edit object (bash stdout, etc.) or a bare string; we only surface results
// that carry a file path or a structured patch.
func decodeToolUseResult(raw json.RawMessage) *ToolResult {
	if len(raw) == 0 {
		return nil
	}
	var rt rawToolResult
	if json.Unmarshal(raw, &rt) != nil {
		return nil
	}
	if rt.FilePath == "" && len(rt.StructuredPatch) == 0 {
		return nil
	}
	tr := &ToolResult{FilePath: rt.FilePath, Kind: rt.Kind}
	for _, h := range rt.StructuredPatch {
		tr.StructuredPatch = append(tr.StructuredPatch, PatchHunk{Lines: h.Lines})
	}
	return tr
}

// canaryCheck records format drift: unknown line/block types, version changes,
// and absence of the load-bearing fields that are always present today (assistant
// usage + its four token keys, and a timestamp on message-bearing lines). It does
// NOT flag benign optional fields (is_error, attribution, structuredPatch).
func canaryCheck(rl *rawLine, ev TranscriptEvent, c *Canary) {
	if rl.Type != "" && !knownLineTypes[rl.Type] {
		c.UnknownLineTypes[rl.Type]++
	}
	if rl.Version != "" {
		if !slices.Contains(c.VersionsSeen, rl.Version) {
			c.VersionsSeen = append(c.VersionsSeen, rl.Version)
		}
		c.Version = rl.Version
	}
	if ev.Message == nil {
		return
	}
	for _, b := range ev.Message.Content {
		if b.Type != "" && !knownBlockTypes[b.Type] {
			c.UnknownBlockTypes[b.Type]++
		}
	}
	if ev.Timestamp.IsZero() {
		c.MissingFields["timestamp"]++
	}
	if rl.Type == "assistant" {
		if rl.Message.Usage == nil {
			c.MissingFields["assistant.usage"]++
		} else {
			u := rl.Message.Usage
			if u.Input == nil {
				c.MissingFields["assistant.usage.input_tokens"]++
			}
			if u.Output == nil {
				c.MissingFields["assistant.usage.output_tokens"]++
			}
			if u.CacheCreation == nil {
				c.MissingFields["assistant.usage.cache_creation_input_tokens"]++
			}
			if u.CacheRead == nil {
				c.MissingFields["assistant.usage.cache_read_input_tokens"]++
			}
		}
	}
}

func (rm *rawMessage) toMessage(c *Canary) *Message {
	if rm == nil {
		return nil
	}
	m := &Message{ID: rm.ID, Role: rm.Role, Model: rm.Model, Content: normalizeContent(rm.Content, c)}
	if rm.Usage != nil {
		m.Usage = &Usage{
			Input:         derefInt(rm.Usage.Input),
			Output:        derefInt(rm.Usage.Output),
			CacheCreation: derefInt(rm.Usage.CacheCreation),
			CacheRead:     derefInt(rm.Usage.CacheRead),
		}
	}
	return m
}

func normalizeContent(raw json.RawMessage, c *Canary) []ContentBlock {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []ContentBlock{{Type: "text", Text: s}}
	}
	var blocks []rawBlock
	if json.Unmarshal(raw, &blocks) == nil {
		out := make([]ContentBlock, 0, len(blocks))
		for _, b := range blocks {
			out = append(out, b.toContentBlock(c))
		}
		return out
	}
	c.MalformedFields["content"]++
	return nil
}

func (b rawBlock) toContentBlock(c *Canary) ContentBlock {
	cb := ContentBlock{Type: b.Type, ToolName: b.Name, ToolInput: b.Input, ToolResult: flattenBody(b.Content)}
	if b.Type == "thinking" {
		cb.Text = b.Thinking
	} else {
		cb.Text = b.Text
	}
	if len(b.IsError) > 0 {
		cb.HasIsError = true
		var v bool
		if json.Unmarshal(b.IsError, &v) == nil {
			cb.IsError = v
		} else {
			c.MalformedFields["is_error"]++
		}
	}
	return cb
}

func flattenBody(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var sb strings.Builder
		for _, p := range parts {
			sb.WriteString(p.Text)
		}
		return sb.String()
	}
	return ""
}
