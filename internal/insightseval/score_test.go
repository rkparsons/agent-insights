package insightseval

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// queueMatcher pops scripted results per call; errors are also scriptable.
type queueMatcher struct {
	results []MatchResult
	errs    []error
	calls   int
}

func (q *queueMatcher) Match(_ context.Context, p MatchPayload) (MatchResult, error) {
	i := q.calls
	q.calls++
	var err error
	if i < len(q.errs) {
		err = q.errs[i]
	}
	var res MatchResult
	if i < len(q.results) {
		res = q.results[i]
	}
	return res, err
}

func scoreRubric() Rubric {
	return Rubric{ID: "C-77", Part: "regression", Tier: "HIGH", Surface: "either",
		Repos: []string{"client-project"}, PassAt: "full", Hash: "rh1",
		Statement:                "verify before asserting",
		RequiredNuances:          []string{"seek contradicting evidence"},
		ForbiddenGeneralizations: []string{"never assert anything"}}
}

func scoreItems() []ScoredItem {
	return []ScoredItem{
		{ID: "client-project/theme/0", Bucket: "client-project", Surface: "theme", Text: "Verify claims",
			SessionIDs: []string{"a1", "a2", "x1"}, Quotes: []string{"q"}},
		{ID: "tmux-ctrl/theme/0", Bucket: "tmux-ctrl", Surface: "theme", Text: "Verify claims elsewhere",
			SessionIDs: []string{"z1"}},
	}
}

func match(id, gran string, nuances []bool, forbidden ...int) ItemMatch {
	if forbidden == nil {
		forbidden = []int{}
	}
	return ItemMatch{ItemID: id, Granularity: gran, NuanceResults: nuances, ForbiddenFormsMatched: forbidden}
}

func TestMedianGranularity(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"full", "full", "absent"}, "full"},
		{[]string{"full", "absent", "absent"}, "absent"},
		{[]string{"full", "partial", "absent"}, "partial"}, // 3-way split → middle
		{[]string{"full", "absent"}, "absent"},             // even → lower middle (conservative)
		{[]string{"partial"}, "partial"},
	}
	for _, c := range cases {
		if got := medianGranularity(c.in); got != c.want {
			t.Errorf("median(%v) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestMatchOnceCachesValidatesAndRetries(t *testing.T) {
	cache := NewCache(t.TempDir())
	p := BuildMatchPayload(scoreRubric(), scoreItems())
	good := MatchResult{Matches: []ItemMatch{match("client-project/theme/0", "full", []bool{true})}}

	// transient error then success within one call's attempts
	q := &queueMatcher{results: []MatchResult{{}, good}, errs: []error{errors.New("boom"), nil}}
	res, hit, err := matchOnce(context.Background(), cache, q, "env1", p, 0)
	if err != nil || hit || len(res.Matches) != 1 {
		t.Fatalf("retry path: %v %v %+v", err, hit, res)
	}
	if q.calls != 2 {
		t.Fatalf("calls = %d", q.calls)
	}
	// cached on the second read — same payload+repeat, no matcher call
	res2, hit2, err := matchOnce(context.Background(), cache, q, "env1", p, 0)
	if err != nil || !hit2 || !reflect.DeepEqual(res, res2) {
		t.Fatalf("cache path: %v %v", err, hit2)
	}
	if q.calls != 2 {
		t.Fatalf("cache must not call the matcher, calls = %d", q.calls)
	}
	// distinct repeat index → distinct key
	if _, hit3, _ := matchOnce(context.Background(), cache, q, "env1", p, 1); hit3 {
		t.Fatal("repeat 1 must not hit repeat 0's entry")
	}

	// invalid output (unknown item) exhausts attempts and errors
	bad := MatchResult{Matches: []ItemMatch{match("nope/theme/9", "full", []bool{true})}}
	qBad := &queueMatcher{results: []MatchResult{bad, bad, bad}}
	if _, _, err := matchOnce(context.Background(), cache, qBad, "env2", p, 0); err == nil {
		t.Fatal("invalid matcher output must fail after attempts")
	}
	if qBad.calls != matcherAttempts {
		t.Fatalf("attempts = %d", qBad.calls)
	}
}

func TestScoreTargetSampleMajorityAndDetail(t *testing.T) {
	cache := NewCache(t.TempDir())
	r := scoreRubric()
	items := scoreItems()
	full := MatchResult{Matches: []ItemMatch{match("client-project/theme/0", "full", []bool{true})}}
	absent := MatchResult{}
	q := &queueMatcher{results: []MatchResult{full, absent, full}}
	s, err := scoreTargetSample(context.Background(), cache, q, "env1", r, items, []string{"a1", "a2"}, nil, nil, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if s.Granularity != "full" || s.RepeatAgreement < 0.66 || s.RepeatAgreement > 0.67 {
		t.Fatalf("sample: %+v", s)
	}
	if s.ItemRef != "client-project/theme/0" || s.Corroboration != CorroborationOK || len(s.ItemQuotes) != 1 {
		t.Fatalf("deciding detail: %+v", s)
	}
}

func TestAggregateRepeatRules(t *testing.T) {
	r := scoreRubric()
	items := map[string]ScoredItem{}
	for _, it := range scoreItems() {
		items[it.ID] = it
	}
	anchors := []string{"a1", "a2"}

	// full match with all nuances but a forbidden hit elsewhere caps the target
	res := MatchResult{Matches: []ItemMatch{
		match("client-project/theme/0", "full", []bool{true}),
		match("tmux-ctrl/theme/0", "partial", []bool{false}, 0),
	}}
	rep := aggregateRepeat(r, items, res, anchors, nil, nil)
	if rep.Granularity != "over_generalized" {
		t.Fatalf("forbidden hit must cap the whole target: %+v", rep)
	}

	// full granularity without all nuances downgrades to partial
	res = MatchResult{Matches: []ItemMatch{match("client-project/theme/0", "full", []bool{false})}}
	if rep = aggregateRepeat(r, items, res, anchors, nil, nil); rep.Granularity != "partial" {
		t.Fatalf("nuance downgrade: %+v", rep)
	}

	// anchor mismatch is never counted → absent, but kept as a side match
	res = MatchResult{Matches: []ItemMatch{match("client-project/theme/0", "full", []bool{true})}}
	rep = aggregateRepeat(r, items, res, []string{"b1", "b2", "b3", "b4"}, nil, nil)
	if rep.Granularity != "absent" || len(rep.SideMatches) != 1 || rep.SideMatches[0].Corroboration != CorroborationMismatch {
		t.Fatalf("uncounted mismatch: %+v", rep)
	}

	// an accepted adjudication makes the same item count
	k := AdjKey{TargetID: "C-77", Statement: normalizeStatement("Verify claims"),
		IDSetHash: idSetHash([]string{"a1", "a2", "x1"}), RubricHash: "rh1", Trigger: CorroborationMismatch}
	adj := map[string]Adjudication{k.Hash(): {Key: k, KeyHash: k.Hash(), Decision: "accept"}}
	rep = aggregateRepeat(r, items, res, []string{"b1", "b2", "b3", "b4"}, nil, adj)
	if rep.Granularity != "full" || len(rep.AdjApplied) != 1 {
		t.Fatalf("adjudicated mismatch must count: %+v", rep)
	}

	// cross-bucket match: never counted, always a side match
	res = MatchResult{Matches: []ItemMatch{match("tmux-ctrl/theme/0", "full", []bool{true})}}
	rep = aggregateRepeat(r, items, res, anchors, nil, nil)
	if rep.Granularity != "absent" || len(rep.SideMatches) != 1 || rep.SideMatches[0].Corroboration != CorroborationCrossBucket {
		t.Fatalf("cross bucket: %+v", rep)
	}

	// no-anchor rubric: match counts (first-pass carding is Task 8's job)
	res = MatchResult{Matches: []ItemMatch{match("client-project/theme/0", "partial", []bool{false})}}
	rep = aggregateRepeat(r, items, res, nil, nil, nil)
	if rep.Granularity != "partial" || rep.Corroboration != CorroborationNoAnchors {
		t.Fatalf("no-anchor count: %+v", rep)
	}
}

func TestScoreTargetSampleEmptyPayloadSkipsMatcher(t *testing.T) {
	q := &queueMatcher{}
	r := scoreRubric()
	r.Surface = "recommendation" // fixture items are all themes → empty payload
	s, err := scoreTargetSample(context.Background(), NewCache(t.TempDir()), q, "env1", r, scoreItems(), nil, nil, nil, 0, 3)
	if err != nil || s.Granularity != "absent" || q.calls != 0 {
		t.Fatalf("empty payload must be absent without LLM calls: %+v calls=%d err=%v", s, q.calls, err)
	}
}

func TestScoreNegativeSample(t *testing.T) {
	cache := NewCache(t.TempDir())
	neg := Rubric{ID: "N-77", Part: "negative", Statement: "a gofmt hook", Hash: "nh1",
		ForbiddenGeneralizations: []string{"add a hook that runs gofmt after every edit"}}
	hit := MatchResult{Matches: []ItemMatch{match("client-project/theme/0", "full", []bool{})}}
	q := &queueMatcher{results: []MatchResult{hit, hit, {}}}
	violated, refs, err := scoreNegativeSample(context.Background(), cache, q, "env1", neg, scoreItems(), 3)
	if err != nil || !violated || !reflect.DeepEqual(refs, []string{"client-project/theme/0"}) {
		t.Fatalf("negative: %v %v %v", violated, refs, err)
	}
	q2 := &queueMatcher{results: []MatchResult{hit, {}, {}}}
	violated, _, err = scoreNegativeSample(context.Background(), cache, q2, "env2", neg, scoreItems(), 3)
	if err != nil || violated {
		t.Fatal("1-of-3 match is not a majority violation")
	}
}

func TestScoringRejectsNonPositiveRepeats(t *testing.T) {
	cache := NewCache(t.TempDir())
	q := &queueMatcher{}
	if _, err := scoreTargetSample(context.Background(), cache, q, "env1", scoreRubric(), scoreItems(), nil, nil, nil, 0, 0); err == nil {
		t.Fatal("repeats=0 must error, not panic or score")
	}
	neg := Rubric{ID: "N-77", Part: "negative", Statement: "s", Hash: "nh1"}
	if _, _, err := scoreNegativeSample(context.Background(), cache, q, "env1", neg, scoreItems(), -1); err == nil {
		t.Fatal("negative repeats must error, not silently pass")
	}
	if q.calls != 0 {
		t.Fatalf("guard must fire before any matcher call, got %d", q.calls)
	}
}
