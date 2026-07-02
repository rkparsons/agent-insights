package synthesis

import tea "charm.land/bubbletea/v2"

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
