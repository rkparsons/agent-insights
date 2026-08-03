package eval

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rkparsons/agent-insights/internal/synthesis"
)

// ScoredItem is one produced theme/recommendation prepared for scoring: the
// matcher sees ID/Bucket/Surface/Text; SessionIDs and Quotes stay Go-side for
// corroboration and cards.
type ScoredItem struct {
	ID         string
	Bucket     string
	Surface    string // "theme" | "recommendation"
	Text       string
	SessionIDs []string // deduped, sorted
	Quotes     []string // verified quotes, capped for cards
}

// BuildScoredItems flattens one bucket-sample's verified output. Theme session
// sets come from the persisted Theme.SessionIDs; recommendation session sets
// are recovered from Raw.EvidenceIDs × bundle, because Recommendation persists
// only counts (synthesize.go).
func BuildScoredItems(bucket string, vo VerifiedOutput, bundle synthesis.EvidenceBundle) []ScoredItem {
	var items []ScoredItem
	for i, t := range vo.Synthesis.Themes {
		items = append(items, ScoredItem{
			ID:         fmt.Sprintf("%s/theme/%d", bucket, i),
			Bucket:     bucket,
			Surface:    "theme",
			Text:       t.Title + ". " + t.Summary,
			SessionIDs: sortedSet(t.SessionIDs),
			Quotes:     capStrings(t.Quotes, 2),
		})
	}
	evidence := evidenceSessionIndex(bundle)
	for i, r := range vo.Synthesis.Recommendations {
		var ids []string
		if i < len(vo.Raw.Recommendations) {
			for _, eid := range vo.Raw.Recommendations[i].EvidenceIDs {
				ids = append(ids, evidence[eid]...)
			}
		}
		items = append(items, ScoredItem{
			ID:         fmt.Sprintf("%s/rec/%d", bucket, i),
			Bucket:     bucket,
			Surface:    "recommendation",
			Text:       r.Statement,
			SessionIDs: sortedSet(ids),
			Quotes:     capStrings(r.Quotes, 2),
		})
	}
	return items
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

// BuildMatchPayload assembles the matcher stdin for one rubric over items
// already restricted to the rubric's buckets, filtered to its surface. Nil
// slices are normalized so the payload hash is byte-stable.
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
		p.Items = append(p.Items, MatchItem{ID: it.ID, Bucket: it.Bucket, Surface: it.Surface, Text: it.Text})
	}
	return p
}

func surfaceAllowed(r Rubric, surface string) bool {
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

// bucketOf extracts the bucket prefix of an item ref ("alpha/theme/3" → "alpha").
func bucketOf(ref string) string {
	if i := strings.Index(ref, "/"); i > 0 {
		return ref[:i]
	}
	return ref
}
