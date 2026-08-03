// Package privacy is the repo-wide CI backstop for committed-artifact
// leaks: real developer home paths, session UUIDs, and employer/ticket
// markers that would out this operator's identity if this pipeline is
// published. internal/eval's privacyScan is a narrower, eval-artifact-only
// sibling of this; this one walks every git-tracked file (see scan_test.go).
package privacy

import (
	"regexp"
	"strings"
)

var usersPathRe = regexp.MustCompile(`/Users/([a-z]+)`)
var homePathRe = regexp.MustCompile(`/home/([a-z]+)`)

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
	{"sc-NNNN ticket marker", regexp.MustCompile(`(?i)sc-[0-9]{4,}`).Match},
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
// than the given known-synthetic placeholder (e.g. /Users/dev, /home/user).
func hasRealPath(re *regexp.Regexp, data []byte, syntheticName string) bool {
	for _, m := range re.FindAllSubmatch(data, -1) {
		if string(m[1]) != syntheticName {
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
