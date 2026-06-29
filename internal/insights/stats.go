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
}

func newStatsBuilder(sessionID string, repo RepoResolver) *statsBuilder {
	return &statsBuilder{sessionID: sessionID, repo: repo}
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
	return s
}
