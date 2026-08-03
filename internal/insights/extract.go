package insights

import "tmux-ctrl/internal/transcript"

// Extract runs one pass over the decoded events, producing the reduced input and
// the deterministic stats. canary is the decoder's drift record, embedded in the
// stats. repo resolves the cwd to a repo identity.
func Extract(events []transcript.TranscriptEvent, canary transcript.Canary, sessionID string, repo RepoResolver) SessionExtraction {
	return extractWithBudget(events, canary, sessionID, repo, defaultBudget)
}

func extractWithBudget(events []transcript.TranscriptEvent, canary transcript.Canary, sessionID string, repo RepoResolver, budget int) SessionExtraction {
	sb := newStatsBuilder(sessionID, repo)
	var vb verbatimBuilder
	var rb reducerBuilder
	for _, ev := range events {
		sb.add(ev)
		vb.add(ev)
		rb.add(ev)
	}
	stats := sb.finish()
	stats.Canary = canary
	return SessionExtraction{
		Stats:    stats,
		Verbatim: vb.finish(),
		Reduced:  rb.finish(stats, budget),
	}
}
