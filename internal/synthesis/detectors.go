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

const (
	// retypeThreshold is probe-pinned (2026-07-09): 0.5 admits paste junk,
	// 0.7 fragments genuine rituals.
	retypeThreshold = 0.6
	kickoffFraction = 0.5
)

type OppSignalInput struct {
	Members []string
	Detail  []string
}

type retypeForm struct {
	norm     string
	exemplar string
	tokens   map[string]int
	occs     int
	first    int
	sessions map[string]bool
}

// retypingSignals clusters directive clauses cross-session: exact-norm merge,
// then single-link components over token-multiset Jaccard >= retypeThreshold
// (order-independent). Only clusters spanning >= signalFloor sessions
// contribute — 2-session echoes are coincidence, not ritual (probe-validated).
func retypingSignals(group []insights.AgentSessionAnalysis) (directives, kickoffs OppSignalInput) {
	byNorm := map[string]*retypeForm{}
	for _, a := range group {
		for _, c := range a.Stats.DirectiveClauses {
			f, ok := byNorm[c.Norm]
			if !ok {
				tokens := map[string]int{}
				for _, t := range insights.ClauseTokens(c.Norm) {
					tokens[t]++
				}
				f = &retypeForm{norm: c.Norm, exemplar: c.Exemplar, tokens: tokens, sessions: map[string]bool{}}
				byNorm[c.Norm] = f
			}
			f.occs += c.Count
			f.first += c.FirstTurn
			f.sessions[a.Stats.SessionID] = true
		}
	}
	forms := make([]*retypeForm, 0, len(byNorm))
	for _, f := range byNorm {
		forms = append(forms, f)
	}
	sort.Slice(forms, func(i, j int) bool { return forms[i].norm < forms[j].norm })

	uf := make([]int, len(forms))
	for i := range uf {
		uf[i] = i
	}
	find := func(x int) int {
		for uf[x] != x {
			uf[x] = uf[uf[x]]
			x = uf[x]
		}
		return x
	}
	for i := 0; i < len(forms); i++ {
		for j := i + 1; j < len(forms); j++ {
			if multisetJaccard(forms[i].tokens, forms[j].tokens) >= retypeThreshold {
				uf[find(i)] = find(j)
			}
		}
	}

	type cluster struct {
		rep, exemplar string
		repOccs       int
		sessions      map[string]bool
		occs, first   int
	}
	byRoot := map[int]*cluster{}
	for i, f := range forms {
		r := find(i)
		cl, ok := byRoot[r]
		if !ok {
			cl = &cluster{sessions: map[string]bool{}}
			byRoot[r] = cl
		}
		for s := range f.sessions {
			cl.sessions[s] = true
		}
		cl.occs += f.occs
		cl.first += f.first
		if f.occs > cl.repOccs || (f.occs == cl.repOccs && (cl.rep == "" || f.norm < cl.rep)) {
			cl.rep, cl.exemplar, cl.repOccs = f.norm, f.exemplar, f.occs
		}
	}
	var clusters []*cluster
	for _, cl := range byRoot {
		if len(cl.sessions) >= signalFloor {
			clusters = append(clusters, cl)
		}
	}
	sort.Slice(clusters, func(i, j int) bool {
		if len(clusters[i].sessions) != len(clusters[j].sessions) {
			return len(clusters[i].sessions) > len(clusters[j].sessions)
		}
		return clusters[i].rep < clusters[j].rep
	})

	build := func(wantKickoff bool) OppSignalInput {
		var in OppSignalInput
		memberSet := map[string]bool{}
		seenSessionSets := map[string]bool{}
		for _, cl := range clusters {
			isKickoff := float64(cl.first) > kickoffFraction*float64(cl.occs)
			if isKickoff != wantKickoff {
				continue
			}
			for s := range cl.sessions {
				memberSet[s] = true
			}
			key := sessionSetKey(cl.sessions)
			if !seenSessionSets[key] && len(in.Detail) < detailCap {
				seenSessionSets[key] = true
				in.Detail = append(in.Detail, cl.exemplar)
			}
		}
		for _, a := range group { // group order = Start-sorted (BuildBundle contract)
			if memberSet[a.Stats.SessionID] {
				in.Members = append(in.Members, a.Stats.SessionID)
			}
		}
		return in
	}
	return build(false), build(true)
}

func sessionSetKey(set map[string]bool) string {
	ids := make([]string, 0, len(set))
	for s := range set {
		ids = append(ids, s)
	}
	sort.Strings(ids)
	return strings.Join(ids, "\x00")
}

func multisetJaccard(a, b map[string]int) float64 {
	inter, sumA, sumB := 0, 0, 0
	for _, n := range a {
		sumA += n
	}
	for _, n := range b {
		sumB += n
	}
	for t, na := range a {
		if nb, ok := b[t]; ok {
			if na < nb {
				inter += na
			} else {
				inter += nb
			}
		}
	}
	union := sumA + sumB - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
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
