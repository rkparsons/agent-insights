package synthesis

import (
	"sort"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

// GlobalDue reports whether a global run is due and which repos contribute new
// sessions. "New" = store analyses whose analyzed-at postdates lastGenerated —
// timestamp-based, never a count delta, so a meta-purge shrinking the store
// can neither mask genuinely new sessions nor drive the count negative. Only
// repos meeting the min_sessions bundle floor count:
// GroupByRepo already drops sub-floor buckets, so a repo absent from groups
// contributes nothing even if every one of its sessions is fresh. Zero-value
// lastGenerated (no snapshot yet) treats every qualifying session as new, so
// due then depends on the threshold alone. contributing is sorted ascending
// and, regardless of due, reflects every qualifying repo with at least one
// fresh session (useful for --dry-run reasoning even when cadence hasn't
// elapsed). cfg must be a post-default insights.LoadConfig() result: this
// function does not re-default its own thresholds, so a zero MinSessions/
// CadenceDays/DueNewSessions here means exactly that.
func GlobalDue(analyses []insights.AgentSessionAnalysis, cfg insights.Config, lastGenerated time.Time, now time.Time) (due bool, contributing []string) {
	groups := GroupByRepo(analyses, cfg.MinSessions, cfg)
	total := 0
	for repo, group := range groups {
		fresh := 0
		for _, a := range group {
			if analyzedAt(a).After(lastGenerated) {
				fresh++
			}
		}
		if fresh > 0 {
			contributing = append(contributing, repo)
			total += fresh
		}
	}
	sort.Strings(contributing)

	cadence := time.Duration(cfg.CadenceDays) * 24 * time.Hour
	cadenceElapsed := lastGenerated.IsZero() || now.Sub(lastGenerated) >= cadence
	due = cadenceElapsed && total >= cfg.DueNewSessions
	return due, contributing
}

// analyzedAt is when an analysis was written: its store-file mtime, stamped at
// load. Analyses loaded from somewhere without one (an eval cache round-trip,
// a hand-built fixture) fall back to the transcript's mtime, which is what
// due-ness used before the store mtime was exposed.
func analyzedAt(a insights.AgentSessionAnalysis) time.Time {
	if !a.AnalyzedAt.IsZero() {
		return a.AnalyzedAt
	}
	return a.TranscriptMtime
}
