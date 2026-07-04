package insightseval

import "regexp"

// Committed-artifact privacy classes (check EVERY class — a repo field once
// leaked a full home path): session ids, cwd/home paths, ticket-branch
// markers, worktree paths. The quote class is handled structurally — verdicts
// carry no free-text fields — this scan is the backstop for runs/ and
// adjudications.json writes.
var privacyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`),
	regexp.MustCompile(`/Users/`),
	regexp.MustCompile(`/home/`),
	regexp.MustCompile(`\$HOME`),
	regexp.MustCompile(`(?i)\bsc-\d+\b`),
	regexp.MustCompile(`\.worktrees/`),
}

// privacyScan returns the sources of the patterns that matched — never the
// matched text, so a finding cannot itself restate the leak.
func privacyScan(data []byte) []string {
	var hits []string
	for _, re := range privacyPatterns {
		if re.Match(data) {
			hits = append(hits, re.String())
		}
	}
	return hits
}
