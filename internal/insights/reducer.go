package insights

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"tmux-ctrl/internal/transcript"
)

const defaultBudget = 160_000 // ~40K tokens; spine is exempt

type reducedRow struct {
	priority int // 0 = spine (always kept)
	ordinal  int
	text     string
}

// reducerBuilder accumulates the reduced-input rows in chronological order. It
// mirrors the stats friction classification so labels stay consistent.
type reducerBuilder struct {
	rows    []reducedRow
	ordinal int
}

func (b *reducerBuilder) spine(text string) {
	b.ordinal++
	b.rows = append(b.rows, reducedRow{0, b.ordinal, text})
}

func (b *reducerBuilder) prose(priority int, text string) {
	b.ordinal++
	b.rows = append(b.rows, reducedRow{priority, b.ordinal, text})
}

func (b *reducerBuilder) add(ev transcript.TranscriptEvent) {
	if ev.Message == nil {
		return
	}
	switch ev.Type {
	case "user":
		b.addUser(ev.Message)
	case "assistant":
		b.addAssistant(ev.Message)
	}
}

func (b *reducerBuilder) addUser(m *transcript.Message) {
	var textParts []string
	for _, blk := range m.Content {
		if blk.Type != "tool_result" {
			if blk.Type == "text" {
				textParts = append(textParts, blk.Text)
			}
			continue
		}
		body := blk.ToolResult
		if blk.HasIsError && blk.IsError {
			switch {
			case isInterruptText(body):
				b.spine("[Interrupt]")
			case isRejectionText(body):
				b.spine(rejectedRow(body))
			default:
				b.spine("[ToolError]: " + trimRunes(body, 400))
			}
		} else if isInterruptText(body) {
			b.spine("[Interrupt]")
		}
	}
	joined := strings.TrimSpace(strings.Join(textParts, "\n"))
	if joined == "" {
		return
	}
	switch {
	case isTaskNotification(joined):
		b.spine("[Subagent result]: " + trimRunes(joined, 4000))
	case isInterruptText(joined):
		b.spine("[Interrupt]")
	case isRejectionText(joined):
		b.spine(rejectedRow(joined))
	case isSyntheticUserText(joined):
		// injected pseudo-user content: dropped
	default:
		b.spine("[User]: " + trimRunes(joined, 4000))
	}
}

func (b *reducerBuilder) addAssistant(m *transcript.Message) {
	for _, blk := range m.Content {
		switch blk.Type {
		case "text":
			if t := strings.TrimSpace(blk.Text); t != "" {
				b.prose(2, "[Assistant]: "+t)
			}
		case "thinking":
			if t := strings.TrimSpace(blk.Text); t != "" {
				b.prose(3, "[Thinking]: "+t)
			}
		case "tool_use":
			b.prose(4, toolRow(blk))
		}
	}
}

func rejectedRow(body string) string {
	if reason, ok := rejectionReason(body); ok && reason != "" {
		return "[Rejected]: " + trimRunes(reason, 1000)
	}
	return "[Rejected]"
}

func toolRow(blk transcript.ContentBlock) string {
	key := toolKey(blk)
	if key == "" {
		return "[Tool: " + blk.ToolName + "]"
	}
	return "[Tool: " + blk.ToolName + " " + trimRunes(key, 120) + "]"
}

func toolKey(blk transcript.ContentBlock) string {
	if blk.ToolName == "Agent" {
		if st, ok := blk.ToolInput["subagent_type"].(string); ok && st != "" {
			return st
		}
	}
	for _, k := range []string{"file_path", "path", "command", "pattern", "description"} {
		if v, ok := blk.ToolInput[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// finish assembles the header + spine (always) + budget-filled prose, in
// chronological order. The spine is never trimmed by budget.
func (b *reducerBuilder) finish(stats AgentSessionStats, budget int) ReducedInput {
	header := buildHeader(stats)
	avail := budget - len(header)

	var spine, rest []reducedRow
	for _, r := range b.rows {
		if r.priority == 0 {
			spine = append(spine, r)
		} else {
			rest = append(rest, r)
		}
	}

	spineChars := 0
	for _, r := range spine {
		spineChars += len(r.text) + 1
	}
	remaining := max(avail-spineChars, 0)

	var keptRest []reducedRow
	for _, pr := range []int{2, 3, 4} {
		for _, r := range rest {
			if r.priority != pr {
				continue
			}
			if remaining-(len(r.text)+1) < 0 {
				break
			}
			keptRest = append(keptRest, r)
			remaining -= len(r.text) + 1
		}
	}

	kept := make([]reducedRow, 0, len(spine)+len(keptRest))
	kept = append(kept, spine...)
	kept = append(kept, keptRest...)
	sort.Slice(kept, func(i, j int) bool { return kept[i].ordinal < kept[j].ordinal })

	var sb strings.Builder
	sb.WriteString(header)
	for _, r := range kept {
		sb.WriteByte('\n')
		sb.WriteString(r.text)
	}
	dropped := len(b.rows) - len(kept)
	if dropped > 0 {
		fmt.Fprintf(&sb, "\n---\n[reduction: kept %d/%d events; %d lower-priority assistant events dropped to fit budget]", len(kept), len(b.rows), dropped)
	}
	text := sb.String()
	return ReducedInput{Text: text, Chars: len(text), KeptEvents: len(kept), DroppedEvents: dropped}
}

func buildHeader(s AgentSessionStats) string {
	id := s.SessionID
	if len(id) > 8 {
		id = id[:8]
	}
	title := s.AiTitle
	if title == "" {
		title = "(none)"
	}
	dur := fmt.Sprintf("%d min", int(math.Round(s.WallClock.Minutes())))
	lines := []string{
		"Session: " + id,
		"Title: " + title,
		"Project: " + s.Cwd + "   Branch: " + s.GitBranch,
		"Duration: " + dur + "   Models: " + strings.Join(sortedIntKeys(s.ModelMix), ","),
		fmt.Sprintf("User turns: %d   Assistant turns: %d   Tool errors: %d   Interrupts: %d   Rejections: %d",
			s.UserTurns, s.AssistantTurns, s.ToolErrors, s.Interrupts, s.Rejections),
		"Top tools: " + topTools(s.ToolCounts, 8),
		"---",
	}
	return strings.Join(lines, "\n")
}

func sortedIntKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func topTools(m map[string]int, n int) string {
	type kv struct {
		name  string
		count int
	}
	pairs := make([]kv, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].name < pairs[j].name
	})
	if len(pairs) > n {
		pairs = pairs[:n]
	}
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = fmt.Sprintf("%s=%d", p.name, p.count)
	}
	return strings.Join(parts, ", ")
}

func trimRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	half := n / 2
	return string(r[:half]) + fmt.Sprintf(" …[%d chars cut]… ", len(r)-n) + string(r[len(r)-half:])
}
