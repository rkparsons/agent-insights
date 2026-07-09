package synthesis

import (
	"sort"
	"strings"

	"tmux-ctrl/internal/insights"
)

const detailCap = 8

var namedMechanicalModes = []string{"edit_before_read", "wrong_cwd", "permission", "symlink_edit"}

// mechanicalFrictionMembers derives the mechanical_friction signal inputs:
// members are sessions with >= 1 error in a NAMED mode (the other-residual is
// drift visibility, not membership), detail lines are "mode — exemplar"
// ranked by aggregate count desc (ties lexicographic), plus the top residual
// signatures. Group is already Start-sorted by BuildBundle.
func mechanicalFrictionMembers(group []insights.AgentSessionAnalysis) (members, detail []string) {
	modeCounts := map[string]int{}
	modeExemplars := map[string]string{}
	sigCounts := map[string]int{}
	for _, a := range group {
		named := 0
		for _, m := range namedMechanicalModes {
			n := a.Stats.MechanicalFriction[m]
			named += n
			modeCounts[m] += n
			if n > 0 && modeExemplars[m] == "" {
				modeExemplars[m] = a.Stats.MechanicalExemplars[m]
			}
		}
		for sig, n := range a.Stats.OtherErrorSignatures {
			sigCounts[sig] += n
		}
		if named > 0 {
			members = append(members, a.Stats.SessionID)
		}
	}
	for _, m := range presentKeysByCount(modeCounts) {
		if ex := modeExemplars[m]; ex != "" {
			detail = append(detail, m+" — "+ex)
		} else {
			detail = append(detail, m)
		}
	}
	if sigs := presentKeysByCount(sigCounts); len(sigs) > 0 {
		if len(sigs) > 3 {
			sigs = sigs[:3]
		}
		detail = append(detail, "residual signatures: "+strings.Join(sigs, "; "))
	}
	if len(detail) > detailCap {
		detail = detail[:detailCap]
	}
	return members, detail
}

func presentKeysByCount(counts map[string]int) []string {
	var keys []string
	for k, n := range counts {
		if n > 0 {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}
