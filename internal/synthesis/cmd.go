package synthesis

import (
	"time"

	"tmux-ctrl/internal/insights"

	tea "charm.land/bubbletea/v2"
)

// SynthesesLoadedMsg carries the result of a periodic LoadSyntheses fetch.
type SynthesesLoadedMsg struct {
	Syntheses []RepoSynthesis
	Err       error
}

// LoadSynthesesCmd dispatches a SynthesesLoadedMsg with the newest per-repo
// synthesis artifacts.
func LoadSynthesesCmd() tea.Cmd {
	return func() tea.Msg {
		s, err := LoadSyntheses()
		return SynthesesLoadedMsg{Syntheses: s, Err: err}
	}
}

type AutoStateMsg struct{ State AutoState }

// AutoStateCmd derives the INSIGHTS automation state; load errors degrade to
// an empty due list rather than failing the tick.
func AutoStateCmd(cadence time.Duration) tea.Cmd {
	return func() tea.Msg {
		if cadence == 0 {
			cadence = DefaultCadence
		}
		var due []string
		analyses, err := LoadAnalyses()
		if err == nil {
			syntheses, serr := LoadSyntheses()
			if serr == nil {
				due = DueRepos(GroupByRepo(analyses, DefaultMinSessions), syntheses, cadence, time.Now())
			}
		}
		rs, has := ReadRunState()
		return AutoStateMsg{State: DeriveAutoState(rs, has, insights.LockHeld(), due)}
	}
}
