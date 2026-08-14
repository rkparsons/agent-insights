package eval

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
)

// globalFixture is one v2 snapshot over two repos: a merged alpha+beta
// finding, an alpha-only finding, and one dropped entry — the shapes carding
// has to handle (one card per finding regardless of repo count, dropped
// entries carded too so a wrongly-dropped finding scores as a recall miss).
func globalFixture() (insights.GlobalSynthesisJSON, map[string]synthesis.EvidenceBundle) {
	bundles := map[string]synthesis.EvidenceBundle{
		"alpha": {
			Repo:     "alpha",
			Friction: []synthesis.FrictionItem{{ID: "F1", OneLine: "took a detour", SessionID: "sA1"}},
			Prefs:    []synthesis.PrefItem{{ID: "P1", Rule: "no comments", SessionID: "sA2"}},
			Signals:  []synthesis.OppSignal{{ID: "G1", Kind: "high_read", MemberSessions: []string{"sA1", "sA3"}}},
		},
		"beta": {
			Repo:     "beta",
			Friction: []synthesis.FrictionItem{{ID: "F1", OneLine: "diffed stale base", SessionID: "sB1"}},
			Success:  []synthesis.SuccessItem{{ID: "S1", Summary: "clean landing", SessionID: "sB2"}},
		},
	}
	snap := insights.GlobalSynthesisJSON{
		SchemaVersion: 2,
		Findings: []insights.FindingJSON{
			{Rank: 1, Title: "Verify first", Statement: "verify before asserting",
				EvidenceIDs: []string{"alpha/F1", "beta/F1", "alpha/F1", "alpha/F9"},
				Repos:       []string{"alpha", "beta"},
				Quotes:      []string{"q1", "q2", "q3"}},
			{Rank: 2, Title: "Read less", Statement: "stop re-reading the same file",
				EvidenceIDs: []string{"alpha/G1"}, Repos: []string{"alpha"}},
		},
		Dropped: []insights.DroppedJSON{
			{Summary: "comment-style nit", Reason: "one session only",
				EvidenceIDs: []string{"alpha/P1"}},
		},
	}
	return snap, bundles
}

// A cross-repo finding is ONE card carrying both repos, not one per bucket.
func TestBuildGlobalScoredItemsCardsEachFindingOnce(t *testing.T) {
	snap, bundles := globalFixture()
	items := BuildGlobalScoredItems(snap, bundles)
	if len(items) != 3 { // 2 findings + 1 dropped
		t.Fatalf("items = %d: %+v", len(items), items)
	}
	merged := items[0]
	if merged.ID != "finding/1" || merged.Surface != surfaceFinding || merged.Dropped {
		t.Fatalf("merged finding item: %+v", merged)
	}
	if merged.Text != "Verify first. verify before asserting" {
		t.Fatalf("finding text: %q", merged.Text)
	}
	if !reflect.DeepEqual(merged.Repos, []string{"alpha", "beta"}) {
		t.Fatalf("merged repos: %v", merged.Repos)
	}
	// alpha/F1→sA1, beta/F1→sB1; the duplicate citation dedupes and the
	// dangling alpha/F9 contributes nothing
	if !reflect.DeepEqual(merged.SessionIDs, []string{"sA1", "sB1"}) {
		t.Fatalf("merged sessions: %v", merged.SessionIDs)
	}
	if len(merged.Quotes) != 2 { // capped for cards
		t.Fatalf("quotes: %v", merged.Quotes)
	}
	// signal citations fan out to member sessions, exactly as the verifier counts them
	if !reflect.DeepEqual(items[1].SessionIDs, []string{"sA1", "sA3"}) {
		t.Fatalf("signal-cited sessions: %v", items[1].SessionIDs)
	}
}

// A dropped entry cards, flagged, with its reason carried for recognition.
func TestBuildGlobalScoredItemsFlagsDropped(t *testing.T) {
	snap, bundles := globalFixture()
	items := BuildGlobalScoredItems(snap, bundles)
	d := items[len(items)-1]
	if d.ID != "dropped/0" || !d.Dropped {
		t.Fatalf("dropped item: %+v", d)
	}
	if d.Text != "comment-style nit" || d.DropReason != "one session only" {
		t.Fatalf("dropped recognition surface: %+v", d)
	}
	if !reflect.DeepEqual(d.Repos, []string{"alpha"}) || !reflect.DeepEqual(d.SessionIDs, []string{"sA2"}) {
		t.Fatalf("dropped provenance: %+v", d)
	}
}

func TestBuildMatchPayloadCarriesReposAndIsDeterministic(t *testing.T) {
	snap, bundles := globalFixture()
	items := BuildGlobalScoredItems(snap, bundles)
	r := Rubric{ID: "X", Part: "regression", Surface: "theme", Repos: []string{"alpha"},
		Statement: "s", RequiredNuances: []string{"n1"}}
	p := BuildMatchPayload(r, items)
	// v1's per-surface filter must not empty a v2 payload: findings are the
	// only pipeline surface now, and every one of them is a candidate.
	if len(p.Items) != 3 {
		t.Fatalf("v2 items must survive a v1 surface value: %+v", p.Items)
	}
	if !reflect.DeepEqual(p.Items[0].Repos, []string{"alpha", "beta"}) {
		t.Fatalf("payload repos: %+v", p.Items[0])
	}
	if p.Rubric.ForbiddenGeneralizations == nil || p.Rubric.RequiredNuances == nil {
		t.Fatal("nil slices must be normalized for stable payload hashes")
	}
	j1, _ := json.Marshal(BuildMatchPayload(r, items))
	j2, _ := json.Marshal(BuildMatchPayload(r, items))
	if string(j1) != string(j2) {
		t.Fatal("payload marshal must be byte-stable")
	}
}

func TestSmallSetHelpers(t *testing.T) {
	if got := sortedSet([]string{"b", "a", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("sortedSet: %v", got)
	}
	if allTrue([]bool{true, false}) || !allTrue(nil) {
		t.Fatal("allTrue")
	}
}
