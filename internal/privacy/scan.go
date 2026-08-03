// Package privacy is the repo-wide CI backstop for committed-artifact
// leaks: real developer home paths, session UUIDs, and employer/ticket
// markers that would out this operator's identity if this pipeline is
// published. internal/eval's privacyScan is a sibling, not a subset: that
// one additionally catches $HOME and .worktrees/ (which this doesn't) and
// this one additionally catches client-project/dev/terminal-app (which that
// doesn't) — eval's scans eval-artifact writes only; this one walks every
// git-tracked file (see scan_test.go). Neither supersedes the other.
package privacy

import (
	"regexp"
	"strings"
)

// Case-insensitive and not restricted to [a-z]: real macOS/Linux usernames
// can be mixed-case or contain digits (e.g. /Users/Richard2), and a
// lowercase-only charset would silently let those through.
var usersPathRe = regexp.MustCompile(`(?i)/Users/([a-zA-Z0-9._-]+)`)
var homePathRe = regexp.MustCompile(`(?i)/home/([a-zA-Z0-9._-]+)`)

// uuidV4Re matches an RFC-4122 v4 UUID (version nibble 4, variant nibble
// 8-b) case-insensitively.
var uuidV4Re = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`)

// check pairs a human-readable finding label (never the matched text itself
// — a scan failure must not restate the leak) with the predicate that
// detects it.
type check struct {
	label string
	match func([]byte) bool
}

var checks = []check{
	{"/Users/<name> path (except /Users/dev)", hasRealUsersPath},
	{"/home/<name> path (except /home/user)", hasRealHomePath},
	{"real UUID-v4 (except 00000000-/0abc1234- synthetic forms)", hasRealUUID},
	{"client-project", regexp.MustCompile(`(?i)client-project`).Match},
	{"sc-NNNN ticket marker", regexp.MustCompile(`(?i)\bsc-[0-9]{4,}\b`).Match},
	{"dev", regexp.MustCompile(`(?i)dev`).Match},
	{"redacted", regexp.MustCompile(`(?i)redacted`).Match},
	{"terminal-app", regexp.MustCompile(`(?i)terminal-app`).Match},
}

// Scan returns the labels of every check that matched data — never the
// matched text, so a finding cannot itself restate the leak.
func Scan(data []byte) []string {
	var hits []string
	for _, c := range checks {
		if c.match(data) {
			hits = append(hits, c.label)
		}
	}
	return hits
}

func hasRealUsersPath(data []byte) bool { return hasRealPath(usersPathRe, data, "dev") }
func hasRealHomePath(data []byte) bool  { return hasRealPath(homePathRe, data, "user") }

// hasRealPath reports whether re matches data with a captured name other
// than the given known-synthetic placeholder (e.g. /Users/dev, /home/user),
// compared case-insensitively since the path itself is matched that way.
func hasRealPath(re *regexp.Regexp, data []byte, syntheticName string) bool {
	for _, m := range re.FindAllSubmatch(data, -1) {
		if !strings.EqualFold(string(m[1]), syntheticName) {
			return true
		}
	}
	return false
}

func hasRealUUID(data []byte) bool {
	for _, m := range uuidV4Re.FindAll(data, -1) {
		s := strings.ToLower(string(m))
		if strings.HasPrefix(s, "00000000-") || strings.HasPrefix(s, "0abc1234-") {
			continue
		}
		return true
	}
	return false
}
