package insights

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

// trivialTurns are content-free user turns that collide across unrelated
// sessions; they are excluded from the fingerprint sequence so Phase-2
// containment matching isn't fooled into collapsing distinct sessions.
var trivialTurns = map[string]bool{
	"yes": true, "y": true, "yep": true, "yeah": true, "ok": true, "okay": true,
	"sure": true, "no": true, "continue": true, "go on": true, "go ahead": true,
	"please continue": true, "proceed": true,
}

func normalizeFingerprintText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func isTrivialTurn(norm string) bool {
	if trivialTurns[norm] {
		return true
	}
	// bare @file mention (single token starting with @)
	return strings.HasPrefix(norm, "@") && !strings.Contains(norm, " ")
}

// fingerprint is a short content hash of a normalised user turn. Content-derived
// (not session-derived) so resume siblings produce matching sequences.
func fingerprint(norm string) string {
	h := sha1.Sum([]byte(norm))
	return hex.EncodeToString(h[:8])
}
