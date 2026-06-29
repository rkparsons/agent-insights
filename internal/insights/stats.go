package insights

import (
	"sort"
	"strings"
	"time"

	"tmux-ctrl/internal/sources/claude"
)

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// statsBuilder accumulates FacetStats across one pass over the decoded events.
type statsBuilder struct {
	sessionID string
	repo      RepoResolver

	cwd       string
	gitBranch string
	aiTitle   string
	version   string

	start    time.Time
	end      time.Time
	startSet bool

	seenMsg        map[string]bool
	assistantTurns int
	modelMix       map[string]int
	tokens         TokenUsage

	toolCounts     map[string]int
	subagentFanout int
	subagentSet    map[string]bool
	skillsSet      map[string]bool
	pluginsSet     map[string]bool
	edits          int
	writes         int
	linesAdded     int
	linesRemoved   int
	filesSet       map[string]bool
}

func newStatsBuilder(sessionID string, repo RepoResolver) *statsBuilder {
	return &statsBuilder{
		sessionID:   sessionID,
		repo:        repo,
		seenMsg:     map[string]bool{},
		modelMix:    map[string]int{},
		toolCounts:  map[string]int{},
		subagentSet: map[string]bool{},
		skillsSet:   map[string]bool{},
		pluginsSet:  map[string]bool{},
		filesSet:    map[string]bool{},
	}
}

func (b *statsBuilder) add(ev claude.TranscriptEvent) {
	if ev.Cwd != "" {
		b.cwd = ev.Cwd
	}
	if ev.GitBranch != "" {
		b.gitBranch = ev.GitBranch
	}
	if ev.AiTitle != "" {
		b.aiTitle = ev.AiTitle
	}
	if ev.Version != "" {
		b.version = ev.Version
	}
	if !ev.Timestamp.IsZero() {
		if !b.startSet {
			b.start = ev.Timestamp
			b.startSet = true
		}
		b.end = ev.Timestamp
	}

	if ev.AttributionSkill != "" {
		b.skillsSet[ev.AttributionSkill] = true
	}
	if ev.AttributionPlugin != "" {
		b.pluginsSet[ev.AttributionPlugin] = true
	}

	if ev.Type == "assistant" && ev.Message != nil {
		b.addAssistantMessage(ev.Message)
		// Tool counts are per-block across ALL lines (a message split across lines
		// carries one block per line), so they are NOT deduped by message.id.
		for _, blk := range ev.Message.Content {
			if blk.Type == "tool_use" {
				b.countTool(blk)
			}
		}
	}

	if ev.ToolUseResult != nil && len(ev.ToolUseResult.StructuredPatch) > 0 {
		if ev.ToolUseResult.FilePath != "" {
			b.filesSet[ev.ToolUseResult.FilePath] = true
		}
		for _, h := range ev.ToolUseResult.StructuredPatch {
			for _, ln := range h.Lines {
				switch {
				case strings.HasPrefix(ln, "+"):
					b.linesAdded++
				case strings.HasPrefix(ln, "-"):
					b.linesRemoved++
				}
			}
		}
	}
}

func (b *statsBuilder) countTool(blk claude.ContentBlock) {
	if blk.ToolName == "" {
		return
	}
	b.toolCounts[blk.ToolName]++
	switch blk.ToolName {
	case "Agent":
		b.subagentFanout++
		if st, ok := blk.ToolInput["subagent_type"].(string); ok && st != "" {
			b.subagentSet[st] = true
		}
	case "Edit":
		b.edits++
	case "Write":
		b.writes++
	}
}

// addAssistantMessage accumulates per-message stats once per distinct message.id.
// One assistant message spans multiple JSONL lines (one per content block) with
// identical usage, so de-duping by id is required to avoid ~2.6-3.5x inflation.
func (b *statsBuilder) addAssistantMessage(m *claude.Message) {
	if m.ID != "" && b.seenMsg[m.ID] {
		return
	}
	if m.ID != "" {
		b.seenMsg[m.ID] = true
	}
	b.assistantTurns++
	if m.Model != "" {
		b.modelMix[m.Model]++
	}
	if m.Usage != nil {
		b.tokens.Input += m.Usage.Input
		b.tokens.Output += m.Usage.Output
		b.tokens.CacheCreation += m.Usage.CacheCreation
		if m.Usage.CacheRead > b.tokens.CacheReadPeak {
			b.tokens.CacheReadPeak = m.Usage.CacheRead
		}
	}
}

func (b *statsBuilder) finish() FacetStats {
	s := FacetStats{
		SessionID: b.sessionID,
		Cwd:       b.cwd,
		GitBranch: b.gitBranch,
		AiTitle:   b.aiTitle,
		Version:   b.version,
		Start:     b.start,
		End:       b.end,
	}
	if b.repo != nil {
		s.Repo = b.repo(b.cwd)
	}
	if b.startSet {
		s.WallClock = b.end.Sub(b.start)
	}
	s.AssistantTurns = b.assistantTurns
	s.ModelMix = b.modelMix
	s.Tokens = b.tokens

	s.ToolCounts = b.toolCounts
	s.SubagentFanout = b.subagentFanout
	s.Subagents = sortedKeys(b.subagentSet)
	s.Skills = sortedKeys(b.skillsSet)
	s.Plugins = sortedKeys(b.pluginsSet)
	s.Edits = b.edits
	s.Writes = b.writes
	s.LinesAdded = b.linesAdded
	s.LinesRemoved = b.linesRemoved
	s.FilesTouched = sortedKeys(b.filesSet)
	return s
}
