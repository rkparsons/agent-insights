package insights

import "tmux-ctrl/internal/sources/claude"

// Extract runs one pass over the decoded events, producing the reduced input and
// the deterministic stats. canary is the decoder's drift record, embedded in the
// stats. repo resolves the cwd to a repo identity.
func Extract(events []claude.TranscriptEvent, canary claude.Canary, sessionID string, repo RepoResolver) Result {
	sb := newStatsBuilder(sessionID, repo)
	var vb verbatimBuilder
	for _, ev := range events {
		sb.add(ev)
		vb.add(ev)
	}
	stats := sb.finish()
	stats.Canary = canary
	return Result{Stats: stats, Verbatim: vb.finish()}
}
