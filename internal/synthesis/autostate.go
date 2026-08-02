package synthesis

import "fmt"

// AutoStatus is the INSIGHTS header's automation status.
type AutoStatus int

const (
	AutoIdle AutoStatus = iota
	AutoDue
	AutoRunning
	AutoError
)

// AutoState is the derived view of the insights automation shown in the
// header/menu.
type AutoState struct {
	Status      AutoStatus
	DueRepos    []string
	LastOutcome string // menu one-liner, e.g. "last run failed: repo: boom"
}

// DeriveAutoState folds run-state, lock, and due-ness into the single INSIGHTS
// header state. A "running" record with a free lock means the run died without
// rewriting its state — treated as failed, not running.
func DeriveAutoState(rs RunState, hasRunState, lockHeld bool, due []string) AutoState {
	st := AutoState{DueRepos: due}
	switch {
	case hasRunState && rs.Status == "running" && lockHeld:
		st.Status = AutoRunning
	case hasRunState && rs.Status == "running":
		st.Status = AutoError
		st.LastOutcome = "last run died (no exit record)"
	case hasRunState && rs.Status == "failed":
		st.Status = AutoError
		st.LastOutcome = "last run failed: " + rs.Reason
	case len(due) > 0:
		st.Status = AutoDue
	default:
		st.Status = AutoIdle
	}
	if hasRunState && rs.Status == "ok" && st.LastOutcome == "" {
		st.LastOutcome = fmt.Sprintf("last run: %d written", rs.Written)
	}
	return st
}
