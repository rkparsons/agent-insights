package insights

import (
	"time"

	"tmux-ctrl/internal/sources/claude"
)

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
}

func newStatsBuilder(sessionID string, repo RepoResolver) *statsBuilder {
	return &statsBuilder{
		sessionID: sessionID,
		repo:      repo,
		seenMsg:   map[string]bool{},
		modelMix:  map[string]int{},
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

	if ev.Type == "assistant" && ev.Message != nil {
		b.addAssistantMessage(ev.Message)
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
	return s
}
