package synthesis

import (
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"testing"

	"tmux-ctrl/internal/insights"
)

func mechSession(id string, modes map[string]int, exemplars map[string]string, sigs map[string]int) insights.AgentSessionAnalysis {
	return insights.AgentSessionAnalysis{Stats: insights.AgentSessionStats{
		SessionID: id, MechanicalFriction: modes, MechanicalExemplars: exemplars, OtherErrorSignatures: sigs,
	}}
}

func TestMechanicalFrictionSignal(t *testing.T) {
	group := []insights.AgentSessionAnalysis{
		mechSession("s1", map[string]int{"edit_before_read": 2, "other": 1},
			map[string]string{"edit_before_read": "File has not been read yet. Read it first before writing to it."},
			map[string]int{"Exit code N": 1}),
		mechSession("s2", map[string]int{"wrong_cwd": 1},
			map[string]string{"wrong_cwd": "no such file or directory: src"}, nil),
		mechSession("s3", map[string]int{"edit_before_read": 1},
			map[string]string{"edit_before_read": "File has not been read yet. Read it first before writing to it."}, nil),
		mechSession("s4", map[string]int{"other": 3}, nil,
			map[string]int{"String to replace not found in file.": 3}),
	}
	members, detail := mechanicalFrictionMembers(group)
	if want := []string{"s1", "s2", "s3"}; !reflect.DeepEqual(members, want) {
		t.Errorf("members = %v, want %v (other-only sessions are visibility, not membership)", members, want)
	}
	wantDetail := []string{
		"edit_before_read — File has not been read yet. Read it first before writing to it.",
		"wrong_cwd — no such file or directory: src",
		"residual signatures: String to replace not found in file.; Exit code N",
	}
	if !reflect.DeepEqual(detail, wantDetail) {
		t.Errorf("detail = %#v\nwant %#v", detail, wantDetail)
	}
}

func TestMechanicalFrictionSignalOrdering(t *testing.T) {
	// wrong_cwd outcounts edit_before_read -> listed first (count desc, ties lex).
	group := []insights.AgentSessionAnalysis{
		mechSession("s1", map[string]int{"wrong_cwd": 5}, map[string]string{"wrong_cwd": "cwd exemplar text here"}, nil),
		mechSession("s2", map[string]int{"edit_before_read": 1, "wrong_cwd": 1},
			map[string]string{"edit_before_read": "read-first exemplar text", "wrong_cwd": "other cwd exemplar"}, nil),
		mechSession("s3", map[string]int{"permission": 1}, nil, nil),
	}
	_, detail := mechanicalFrictionMembers(group)
	want := []string{
		"wrong_cwd — cwd exemplar text here",
		"edit_before_read — read-first exemplar text",
		"permission",
	}
	if !reflect.DeepEqual(detail, want) {
		t.Errorf("detail = %#v\nwant %#v", detail, want)
	}
}

func dirSession(id string, clauses ...insights.DirectiveClause) insights.AgentSessionAnalysis {
	return insights.AgentSessionAnalysis{Stats: insights.AgentSessionStats{
		SessionID: id, DirectiveClauses: clauses,
	}}
}

func dc(norm string, count, first int) insights.DirectiveClause {
	return insights.DirectiveClause{Norm: norm, Exemplar: norm, Count: count, FirstTurn: first}
}

func TestRetypingSignalsClusterAcrossVariants(t *testing.T) {
	// Three near-verbatim variants (multiset sim >= 0.6 pairwise via
	// single-link) across three sessions -> one contributing cluster.
	group := []insights.AgentSessionAnalysis{
		dirSession("s1", dc("leave the branch as it is for manual testing", 1, 0)),
		dirSession("s2", dc("leave the branch as it is for manual testing please", 1, 0)),
		dirSession("s3", dc("leave the branch as it is for my manual testing", 1, 0)),
		dirSession("s4", dc("a completely unrelated clause about weather panels", 1, 0)),
	}
	directives, kickoffs := retypingSignals(group)
	if want := []string{"s1", "s2", "s3"}; !reflect.DeepEqual(directives.Members, want) {
		t.Fatalf("directive members = %v, want %v", directives.Members, want)
	}
	if len(directives.Detail) != 1 || directives.Detail[0] != "leave the branch as it is for manual testing" {
		t.Errorf("detail = %#v (want the highest-occurrence variant as representative)", directives.Detail)
	}
	if len(kickoffs.Members) != 0 {
		t.Errorf("kickoff members = %v, want none", kickoffs.Members)
	}
}

func TestRetypingKindSplit(t *testing.T) {
	// All occurrences in first prose turns -> kickoffs, not directives.
	group := []insights.AgentSessionAnalysis{
		dirSession("s1", dc("please diagnose this panic and fix cleanly", 1, 1)),
		dirSession("s2", dc("please diagnose this panic and fix cleanly", 1, 1)),
		dirSession("s3", dc("please diagnose this panic and fix cleanly", 1, 1)),
	}
	directives, kickoffs := retypingSignals(group)
	if len(directives.Members) != 0 {
		t.Errorf("directives = %v, want none", directives.Members)
	}
	if want := []string{"s1", "s2", "s3"}; !reflect.DeepEqual(kickoffs.Members, want) {
		t.Errorf("kickoffs = %v, want %v", kickoffs.Members, want)
	}
}

func TestRetypeThresholdBoundary(t *testing.T) {
	// sim(X, Y) is exactly 0.6 (inter 6 / union 10): the pair must cluster at
	// the pinned >= 0.6 threshold. Any raised threshold (or a >= -> > flip)
	// splits them below the floor and empties the signal.
	x := "please run the full eval suite before the bump"
	y := "please run the full eval suite tonight"
	group := []insights.AgentSessionAnalysis{
		dirSession("s1", dc(x, 1, 0)),
		dirSession("s2", dc(x, 1, 0)),
		dirSession("s3", dc(y, 1, 0)),
	}
	directives, _ := retypingSignals(group)
	if want := []string{"s1", "s2", "s3"}; !reflect.DeepEqual(directives.Members, want) {
		t.Errorf("members = %v, want %v (boundary pair must cluster at exactly 0.6)", directives.Members, want)
	}
}

func TestRetypingBelowFloorClustersExcluded(t *testing.T) {
	// A 2-session echo must not contribute members or detail.
	group := []insights.AgentSessionAnalysis{
		dirSession("s1", dc("what do you think about this approach", 1, 0)),
		dirSession("s2", dc("what do you think about this approach", 1, 0)),
		dirSession("s3", dc("entirely different clause content here", 1, 0)),
	}
	directives, _ := retypingSignals(group)
	if len(directives.Members) != 0 || len(directives.Detail) != 0 {
		t.Errorf("below-floor cluster leaked: %+v", directives)
	}
}

func TestRetypingSessionOrderIndependence(t *testing.T) {
	mk := func(ids ...string) []insights.AgentSessionAnalysis {
		var g []insights.AgentSessionAnalysis
		for _, id := range ids {
			g = append(g, dirSession(id,
				dc("please assign an opus subagent to do a critical review", 1, 0),
				dc("write the implementation plan for the next phase", 1, 0)))
		}
		return g
	}
	a, _ := retypingSignals(mk("s1", "s2", "s3"))
	b, _ := retypingSignals(mk("s3", "s1", "s2"))
	if !reflect.DeepEqual(a.Detail, b.Detail) {
		t.Errorf("detail order depends on session order: %v vs %v", a.Detail, b.Detail)
	}
	sort.Strings(a.Members)
	sort.Strings(b.Members)
	if !reflect.DeepEqual(a.Members, b.Members) {
		t.Errorf("membership depends on session order")
	}
}

func TestRetypingDetailDedupeAndCap(t *testing.T) {
	// One pasted template = many clauses over an identical session set ->
	// exactly one detail line (probe consequence ii). Norms are pairwise
	// dissimilar so the dedupe path is exercised, not the merge path.
	var g []insights.AgentSessionAnalysis
	for _, id := range []string{"s1", "s2", "s3"} {
		g = append(g, dirSession(id,
			dc("alpha entirely distinct first clause body", 1, 0),
			dc("totally different second ritual invocation text", 1, 0),
			dc("third unrelated directive sentence for testing", 1, 0),
		))
	}
	directives, _ := retypingSignals(g)
	if len(directives.Detail) != 1 {
		t.Errorf("detail = %#v, want 1 line (identical session sets dedupe)", directives.Detail)
	}
	if want := []string{"s1", "s2", "s3"}; !reflect.DeepEqual(directives.Members, want) {
		t.Errorf("members = %v, want %v", directives.Members, want)
	}
}

// Detail's privacy guarantee is structural — everything entering it passed
// insights.SanitizeEvidenceText at stats-build time. This test pins the
// invariant end-to-end over real corpus error texts and clauses carrying
// every committed-artifact privacy class.
func TestDetailPassesCommittedArtifactPrivacyClasses(t *testing.T) {
	cwdErr := insights.SanitizeEvidenceText("File does not exist. Note: your current working directory is /Users/dev/Developer/alpha/.worktrees/preview-issues.")
	clause := insights.SanitizeEvidenceText("resume session 8f3d2a1b-4c5d-6e7f-8a9b-0c1d2e3f4a5b in the /Users/dev/Developer/tmux-ctrl/.worktrees/insights-generation worktree for sc-42")
	group := []insights.AgentSessionAnalysis{
		mechSession("s1", map[string]int{"wrong_cwd": 1}, map[string]string{"wrong_cwd": cwdErr}, nil),
		mechSession("s2", map[string]int{"wrong_cwd": 2}, map[string]string{"wrong_cwd": cwdErr}, nil),
		mechSession("s3", map[string]int{"edit_before_read": 1},
			map[string]string{"edit_before_read": "File has not been read yet. Read it first before writing to it."}, nil),
		dirSession("s4", insights.DirectiveClause{Norm: clause, Exemplar: clause, Count: 1}),
		dirSession("s5", insights.DirectiveClause{Norm: clause, Exemplar: clause, Count: 1}),
		dirSession("s6", insights.DirectiveClause{Norm: clause, Exemplar: clause, Count: 1}),
	}
	b := BuildBundle("r", group)
	var kinds []string
	for _, g := range b.Signals {
		kinds = append(kinds, g.Kind)
	}
	if !reflect.DeepEqual(kinds, []string{"mechanical_friction", "retyped_directives"}) {
		t.Fatalf("signals = %v, want both detector kinds emitted", kinds)
	}
	raw, err := json.Marshal(b.Signals)
	if err != nil {
		t.Fatal(err)
	}
	for _, pat := range []string{`/Users/`, `/home/`, `\$HOME`, `\.worktrees/`,
		`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`, `\bsc-\d+\b`} {
		if regexp.MustCompile(`(?i)` + pat).Match(raw) {
			t.Errorf("privacy class %s present in emitted signals: %s", pat, raw)
		}
	}
}

var digitRE = regexp.MustCompile(`[0-9]`)

func TestMechanicalDetailCarriesNoConstructedNumbers(t *testing.T) {
	group := []insights.AgentSessionAnalysis{
		mechSession("s1", map[string]int{"edit_before_read": 7}, map[string]string{"edit_before_read": "digit-free exemplar"}, nil),
		mechSession("s2", map[string]int{"edit_before_read": 9}, map[string]string{"edit_before_read": "digit-free exemplar"}, nil),
		mechSession("s3", map[string]int{"permission": 4}, nil, nil),
	}
	_, detail := mechanicalFrictionMembers(group)
	for _, d := range detail {
		if digitRE.MatchString(d) {
			t.Errorf("detail line carries a digit not present in any source text: %q", d)
		}
	}
}
