// Package privacy is the repo-wide CI backstop for committed-artifact leaks:
// real developer home paths, session UUIDs, and ticket markers that would out
// this operator's identity if this pipeline is published. internal/eval's
// privacyScan is a sibling, not a subset: that one additionally catches $HOME
// and .worktrees/ (which this doesn't) and this one additionally catches the
// dash-encoded project-slug forms and any configured identity patterns (which
// that doesn't) — eval's scans eval-artifact writes only; this one walks every
// git-tracked file (see scan_test.go). Neither supersedes the other.
//
// Two pattern layers, split by whether the pattern itself is safe to commit:
//
//   - The generic classes below are structural — they describe the *shape* of a
//     home path, a session id, a ticket marker — and name nobody, so they ship
//     in-tree and run everywhere.
//   - Identity tokens (an employer, a username, a renamed personal project)
//     would themselves be the leak if hardcoded in a public tree: a scanner
//     that greps for a company name publishes that company name. Those live in
//     a gitignored pattern file instead, read at scan time from
//     AGENT_INSIGHTS_PRIVATE_PATTERNS, else <repo-root>/.privacy-patterns.
//
// An absent pattern file is not an error — a fresh clone and a CI runner
// legitimately have none, and the generic classes still run. The file is the
// operator's, and the pre-publish scan on their machine is where it bites.
package privacy

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// PrivatePatternsEnv overrides the private-pattern file path.
	PrivatePatternsEnv = "AGENT_INSIGHTS_PRIVATE_PATTERNS"
	// PrivatePatternsFile is the repo-root-relative default, gitignored.
	PrivatePatternsFile = ".privacy-patterns"
)

// Case-insensitive and not restricted to [a-z]: real macOS/Linux usernames can
// be mixed-case or contain digits, and a lowercase-only charset would silently
// let those through.
var usersPathRe = regexp.MustCompile(`(?i)/Users/([a-zA-Z0-9._-]+)`)
var homePathRe = regexp.MustCompile(`(?i)/home/([a-zA-Z0-9._-]+)`)

// Claude Code names each project directory after the session cwd with every
// separator replaced by a dash, so a home path also leaks in a form with no
// slash in it at all: -Users-<name>-Developer-<repo>. The captured name
// charset excludes the dash here precisely because the dash is the separator
// in this encoding.
var usersSlugRe = regexp.MustCompile(`(?i)-Users-([a-zA-Z0-9._]+)`)
var homeSlugRe = regexp.MustCompile(`(?i)-home-([a-zA-Z0-9._]+)`)

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

var genericChecks = []check{
	{"/Users/<name> path (except /Users/dev)", func(d []byte) bool { return hasRealPath(usersPathRe, d, "dev") }},
	{"-Users-<name> project slug (except -Users-dev)", func(d []byte) bool { return hasRealPath(usersSlugRe, d, "dev") }},
	{"/home/<name> path (except /home/user)", func(d []byte) bool { return hasRealPath(homePathRe, d, "user") }},
	{"-home-<name> project slug (except -home-user)", func(d []byte) bool { return hasRealPath(homeSlugRe, d, "user") }},
	{"real UUID-v4 (except 00000000-/0abc1234- synthetic forms)", hasRealUUID},
	{"sc-NNNN ticket marker", regexp.MustCompile(`(?i)\bsc-[0-9]{4,}\b`).Match},
}

// Scanner is a configured scan: the generic classes above plus whatever
// identity patterns were loaded from the private pattern file.
type Scanner struct{ checks []check }

// New returns a Scanner running the generic classes plus private, in order.
// New(nil) is the generic-only scanner a fresh clone gets.
func New(private []*regexp.Regexp) *Scanner {
	checks := make([]check, 0, len(genericChecks)+len(private))
	checks = append(checks, genericChecks...)
	for i, re := range private {
		// The label is the pattern's position, never its source: a private
		// pattern echoed into a CI log is exactly the string the file exists
		// to keep out of the tree.
		checks = append(checks, check{fmt.Sprintf("private identity pattern #%d", i+1), re.Match})
	}
	return &Scanner{checks: checks}
}

// Scan returns the labels of every check that matched data — never the
// matched text, so a finding cannot itself restate the leak.
func (s *Scanner) Scan(data []byte) []string {
	var hits []string
	for _, c := range s.checks {
		if c.match(data) {
			hits = append(hits, c.label)
		}
	}
	return hits
}

// PrivatePatternsPath resolves the pattern file for repoRoot: the
// AGENT_INSIGHTS_PRIVATE_PATTERNS override when set, else
// <repoRoot>/.privacy-patterns.
func PrivatePatternsPath(repoRoot string) string {
	if p := os.Getenv(PrivatePatternsEnv); p != "" {
		return p
	}
	return filepath.Join(repoRoot, PrivatePatternsFile)
}

// LoadPrivatePatterns reads the private pattern file: one Go regexp per line,
// blank lines and #-comments ignored, each compiled case-insensitively. A
// missing file yields (nil, nil) — see the package comment for why that is
// deliberately not an error. A malformed line is an error naming the line
// number, never the line's content.
func LoadPrivatePatterns(repoRoot string) ([]*regexp.Regexp, error) {
	path := PrivatePatternsPath(repoRoot)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*regexp.Regexp
	sc := bufio.NewScanner(bytes.NewReader(data))
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		re, err := regexp.Compile("(?i)" + text)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: invalid pattern", path, line)
		}
		out = append(out, re)
	}
	return out, sc.Err()
}

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
