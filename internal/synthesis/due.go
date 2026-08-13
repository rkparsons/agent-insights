package synthesis

import (
	"sort"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

const DefaultCadence = 14 * 24 * time.Hour

// DueRepos returns the repo keys due for synthesis, sorted ascending. A repo
// is due when it has never been synthesized (groups are already
// min-sessions-filtered), or its latest synthesis is at least cadence old AND
// the analysis set changed since (AnalyzedCount was len(group) at generation;
// != not > because a meta-purge can shrink the store).
func DueRepos(groups map[string][]insights.AgentSessionAnalysis, syntheses []RepoSynthesis, cadence time.Duration, now time.Time) []string {
	latest := make(map[string]RepoSynthesis, len(syntheses))
	for _, s := range syntheses {
		latest[s.Repo] = s
	}
	var due []string
	for k, group := range groups {
		s, ok := latest[k]
		if !ok {
			due = append(due, k)
			continue
		}
		if now.Sub(s.GeneratedAt) >= cadence && len(group) != s.Window.AnalyzedCount {
			due = append(due, k)
		}
	}
	sort.Strings(due)
	return due
}

// GlobalDue reports whether a global run is due and which repos contribute new
// sessions. "New" = store analyses whose analyzed-at postdates lastGenerated
// (timestamp-based — never count deltas; see the v1 hazard comment on DueRepos
// above: a meta-purge shrinking the store must never mask genuinely new
// sessions). The store carries no separate "analyzed at" instant — WriteAnalysis
// never calls time.Now() — so TranscriptMtime (stamped from the raw transcript's
// mtime at decode time, insights.AgentSessionAnalysis's only per-analysis
// timestamp) stands in for it here. Only repos meeting the min_sessions bundle
// floor count: GroupByRepo already drops sub-floor buckets, so a repo absent
// from groups contributes nothing even if every one of its sessions is fresh.
// Zero-value lastGenerated (no snapshot yet) treats every qualifying session as
// new, so due then depends on the threshold alone. contributing is sorted
// ascending and, regardless of due, reflects every qualifying repo with at
// least one fresh session (useful for --dry-run reasoning even when cadence
// hasn't elapsed).
func GlobalDue(analyses []insights.AgentSessionAnalysis, cfg insights.Config, lastGenerated time.Time, now time.Time) (due bool, contributing []string) {
	groups := GroupByRepo(analyses, cfg.MinSessions, cfg)
	total := 0
	for repo, group := range groups {
		fresh := 0
		for _, a := range group {
			if a.TranscriptMtime.After(lastGenerated) {
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
