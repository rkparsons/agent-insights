package eval

import (
	"fmt"
	"sort"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
)

// surfaceFinding is the v2 pipeline's only produced surface. v1's
// theme/recommendation split died with the per-repo synthesis; the constant
// survives because the matcher-integrity probes still shape their synthetic
// items by the rubric's (v1-vocabulary) surface field.
const surfaceFinding = "finding"

// ScoredItem is one produced finding — or one dropped entry — prepared for
// scoring: the matcher sees ID/Repos/Surface/Text; SessionIDs and Quotes stay
// Go-side for corroboration and cards.
//
// Repos is a list, not a bucket: a v2 finding may legitimately merge evidence
// from several repos, and that merge is exactly what the contract rewards.
type ScoredItem struct {
	ID         string
	Repos      []string // repos whose evidence the item cites, sorted
	Surface    string   // "finding" (v2 pipeline) | "theme"/"recommendation" (probes only)
	Dropped    bool     // the model dropped this evidence instead of acting on it
	DropReason string   // dropped only: the model's stated reason, for the card
	Text       string
	SessionIDs []string // deduped, sorted
	Quotes     []string // verified quotes, capped for cards
}

// BuildGlobalScoredItems flattens one v2 snapshot into the sample's global card
// set: one item per finding (however many repos it spans) plus one per dropped
// entry, so a good finding wrongly dropped is scorable as a recall miss rather
// than invisible. Session sets are recovered from the cited namespaced evidence
// ids × bundles — the same derivation the verifier's Go-owned fields use — since
// the snapshot persists counts, not ids.
func BuildGlobalScoredItems(snap insights.GlobalSynthesisJSON, bundles map[string]synthesis.EvidenceBundle) []ScoredItem {
	evidence := globalEvidenceIndex(bundles)
	items := make([]ScoredItem, 0, len(snap.Findings)+len(snap.Dropped))
	for _, f := range snap.Findings {
		sessions, repos := evidence.resolve(f.EvidenceIDs)
		if len(f.Repos) > 0 {
			repos = sortedSet(f.Repos) // Go-owned on the snapshot; prefer it
		}
		items = append(items, ScoredItem{
			// Rank identifies a finding to a human reading a card, and
			// VerifyGlobal guarantees ranks are a gapless 1..N permutation over
			// the rank-sorted array, so it is also unique.
			ID:         fmt.Sprintf("finding/%d", f.Rank),
			Repos:      repos,
			Surface:    surfaceFinding,
			Text:       f.Title + ". " + f.Statement,
			SessionIDs: sessions,
			Quotes:     capStrings(f.Quotes, 2),
		})
	}
	for i, d := range snap.Dropped {
		sessions, repos := evidence.resolve(d.EvidenceIDs)
		items = append(items, ScoredItem{
			ID:         fmt.Sprintf("dropped/%d", i),
			Repos:      repos,
			Surface:    surfaceFinding,
			Dropped:    true,
			DropReason: d.Reason,
			Text:       d.Summary,
			SessionIDs: sessions,
		})
	}
	return items
}

// evidenceIndex maps every namespaced bundle id ("<repo>/F3") to its repo and
// session id(s); G signals fan out to their member sessions.
type evidenceIndex map[string]evidenceRef

type evidenceRef struct {
	repo     string
	sessions []string
}

// resolve turns a citation list into the item's deduped session set and the
// repos it draws from. Unknown ids contribute nothing (the verifier already
// hard-fails dangling citations; a cached snapshot must not panic on one).
func (idx evidenceIndex) resolve(ids []string) (sessions, repos []string) {
	var s, r []string
	for _, id := range ids {
		ref, ok := idx[id]
		if !ok {
			continue
		}
		r = append(r, ref.repo)
		s = append(s, ref.sessions...)
	}
	return sortedSet(s), sortedSet(r)
}

func globalEvidenceIndex(bundles map[string]synthesis.EvidenceBundle) evidenceIndex {
	idx := evidenceIndex{}
	for repo, b := range bundles {
		for id, sessions := range evidenceSessionIndex(b) {
			idx[repo+"/"+id] = evidenceRef{repo: repo, sessions: sessions}
		}
	}
	return idx
}

// evidenceSessionIndex maps every typed bundle id (F/P/S/G) to its session
// id(s); G signals fan out to their member sessions.
func evidenceSessionIndex(b synthesis.EvidenceBundle) map[string][]string {
	out := map[string][]string{}
	for _, f := range b.Friction {
		out[f.ID] = []string{f.SessionID}
	}
	for _, p := range b.Prefs {
		out[p.ID] = []string{p.SessionID}
	}
	for _, s := range b.Success {
		out[s.ID] = []string{s.SessionID}
	}
	for _, g := range b.Signals {
		out[g.ID] = append([]string(nil), g.MemberSessions...)
	}
	return out
}

// BuildMatchPayload assembles the matcher stdin for one rubric over the
// sample's global item set. Nil slices are normalized so the payload hash is
// byte-stable.
func BuildMatchPayload(r Rubric, items []ScoredItem) MatchPayload {
	nuances := r.RequiredNuances
	if nuances == nil {
		nuances = []string{}
	}
	forbidden := r.ForbiddenGeneralizations
	if forbidden == nil {
		forbidden = []string{}
	}
	p := MatchPayload{Rubric: MatchRubric{
		ID: r.ID, Part: r.Part, Statement: r.Statement,
		RequiredNuances: nuances, ForbiddenGeneralizations: forbidden,
	}, Items: []MatchItem{}}
	for _, it := range items {
		if !surfaceAllowed(r, it.Surface) {
			continue
		}
		repos := it.Repos
		if repos == nil {
			repos = []string{}
		}
		p.Items = append(p.Items, MatchItem{ID: it.ID, Repos: repos, Surface: it.Surface, Text: it.Text})
	}
	return p
}

// surfaceAllowed filters probe items by the rubric's v1 surface value. A v2
// finding is never filtered: the pipeline produces one surface, so honoring a
// frozen rubric's "theme"/"recommendation" here would empty every payload and
// score every target absent.
func surfaceAllowed(r Rubric, surface string) bool {
	if surface == surfaceFinding {
		return true
	}
	switch r.Surface {
	case "theme", "recommendation":
		return r.Surface == surface
	default: // "either", and negative rubrics carry no surface
		return true
	}
}

func sortedSet(ids []string) []string {
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func stringSet(ids []string) map[string]bool {
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	return set
}

func capStrings(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func allTrue(bs []bool) bool {
	for _, b := range bs {
		if !b {
			return false
		}
	}
	return true
}
