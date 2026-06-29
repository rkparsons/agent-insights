package insights

import (
	"strings"

	"tmux-ctrl/internal/sources/claude"
)

// VerbatimIndex backs the anti-fabrication quote check. It holds two corpora:
// user-authored prose (for standing_preferences, which must be verbatim user
// words) and the full decoded text (for friction quotes, which may come from
// anywhere). Each is offered exact and whitespace-normalized. The drop/flag
// logic is a later phase; this only provides the capability.
type VerbatimIndex struct {
	userExact string
	allExact  string
	userNorm  string
	allNorm   string
}

func (v VerbatimIndex) ContainsUser(quote string) bool {
	return quote != "" && strings.Contains(v.userExact, quote)
}

func (v VerbatimIndex) ContainsUserNormalized(quote string) bool {
	q := normalizeWS(quote)
	return q != "" && strings.Contains(v.userNorm, q)
}

func (v VerbatimIndex) ContainsAny(quote string) bool {
	return quote != "" && strings.Contains(v.allExact, quote)
}

func (v VerbatimIndex) ContainsAnyNormalized(quote string) bool {
	q := normalizeWS(quote)
	return q != "" && strings.Contains(v.allNorm, q)
}

func normalizeWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

type verbatimBuilder struct {
	user strings.Builder
	all  strings.Builder
}

func (b *verbatimBuilder) addUser(s string) {
	b.user.WriteString(s)
	b.user.WriteByte('\n')
	b.all.WriteString(s)
	b.all.WriteByte('\n')
}

func (b *verbatimBuilder) addAll(s string) {
	b.all.WriteString(s)
	b.all.WriteByte('\n')
}

func (b *verbatimBuilder) add(ev claude.TranscriptEvent) {
	if ev.Message == nil {
		return
	}
	switch ev.Type {
	case "user":
		var parts []string
		for _, blk := range ev.Message.Content {
			switch blk.Type {
			case "text":
				parts = append(parts, blk.Text)
			case "tool_result":
				// Real rejections are is_error tool_results; the reason after
				// "the user said:" is the user's own words.
				if reason, ok := rejectionReason(blk.ToolResult); ok && isRejectionText(blk.ToolResult) {
					b.addUser(reason)
				} else {
					b.addAll(blk.ToolResult) // error/output bodies are quotable friction evidence
				}
			}
		}
		joined := strings.TrimSpace(strings.Join(parts, "\n"))
		if joined == "" {
			return
		}
		switch {
		case isTaskNotification(joined):
			b.addAll(joined)
		case isRejectionText(joined):
			if reason, ok := rejectionReason(joined); ok {
				b.addUser(reason) // the user's own correction words
			}
		case isSyntheticUserText(joined), isInterruptText(joined):
			// not quotable user prose
		default:
			b.addUser(joined)
		}
	case "assistant":
		for _, blk := range ev.Message.Content {
			if blk.Type == "text" || blk.Type == "thinking" {
				b.addAll(blk.Text)
			}
		}
	}
}

func (b *verbatimBuilder) finish() VerbatimIndex {
	u := b.user.String()
	a := b.all.String()
	return VerbatimIndex{
		userExact: u,
		allExact:  a,
		userNorm:  normalizeWS(u),
		allNorm:   normalizeWS(a),
	}
}
