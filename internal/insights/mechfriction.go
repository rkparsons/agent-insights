package insights

import (
	"regexp"
	"strings"
)

// Mechanical-friction modes (Phase-3 detector 1). The pattern table is
// corpus-tuned by design: these are exactly the modes the ground-truth window
// evidenced; unmodeled modes stay visible via the other-residual signatures.
const (
	modeEditBeforeRead = "edit_before_read"
	modeWrongCwd       = "wrong_cwd"
	modePermission     = "permission"
	modeSymlinkEdit    = "symlink_edit"
	modeOther          = "other"
)

// Ordered: first match wins.
var mechanicalPatterns = []struct{ mode, substr string }{
	{modeEditBeforeRead, "File has not been read yet"},
	{modeWrongCwd, "Note: your current working directory is"},
	{modeWrongCwd, "no such file or directory: src"},
	{modeWrongCwd, "does not contain main module"},
	{modeWrongCwd, "cannot find main module"},
	{modeSymlinkEdit, "Refusing to write through symlink"},
}

func classifyMechanicalError(body string) (string, bool) {
	for _, p := range mechanicalPatterns {
		if strings.Contains(body, p.substr) {
			return p.mode, true
		}
	}
	return "", false
}

var (
	evidenceUUIDRE   = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	evidenceTicketRE = regexp.MustCompile(`(?i)\bsc-\d+\b`)
	// Only privacy-sensitive roots are collapsed; benign absolute paths like
	// /dev/null survive so exemplar directives stay meaningful.
	evidencePathRE = regexp.MustCompile(`(\$HOME[^\s]*|/(?:Users|home|private|var|tmp)/[^\s]*|[^\s]*\.worktrees/[^\s]*)`)
	digitsRE       = regexp.MustCompile(`\d+`)
)

// SanitizeEvidenceText makes a raw transcript fragment safe for bundle Detail
// (and thence cards/verdict quotes): committed-artifact privacy classes are
// replaced, never carried (privacy.go classes: id/path/ticket). A collapsed
// path swallows its trailing sentence period; that is acceptable.
func SanitizeEvidenceText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<tool_use_error>")
	s = strings.TrimSuffix(s, "</tool_use_error>")
	s = evidenceUUIDRE.ReplaceAllString(s, "[id]")
	s = evidenceTicketRE.ReplaceAllString(s, "[ticket]")
	s = evidencePathRE.ReplaceAllString(s, "[path]")
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 160 {
		s = string(r[:160])
	}
	return s
}

// errorSignature buckets a residual error body for drift visibility: first
// line, sanitized, digits collapsed so retry/exit-code variants converge.
func errorSignature(body string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	sig := digitsRE.ReplaceAllString(SanitizeEvidenceText(first), "N")
	if r := []rune(sig); len(r) > 120 {
		sig = string(r[:120])
	}
	return sig
}
