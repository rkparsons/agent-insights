package insights

import (
	"reflect"
	"testing"
)

func TestExtractClauses(t *testing.T) {
	cases := []struct {
		name string
		turn string
		want []string
	}{
		{"sentence terminators split, trivia dropped",
			"Please assign an opus subagent to do a critical review. Then run them! Ok?",
			[]string{"Please assign an opus subagent to do a critical review."}},
		{"newlines split",
			"leave the branch as-is for manual testing\nwrite the implementation plan afterwards",
			[]string{"leave the branch as-is for manual testing", "write the implementation plan afterwards"}},
		{"code fences excluded",
			"apply this exact diff to the parser.\n```\nrm -rf the entire everything\n```\nthen leave the branch as-is please",
			[]string{"apply this exact diff to the parser.", "then leave the branch as-is please"}},
		{"unclosed fence drops the rest",
			"run the following commands now:\n```\nnever emitted as a clause",
			[]string{"run the following commands now:"}},
		{"log lines excluded: timestamp prefix and letterless",
			"2026-07-09T10:00:01Z worker panicked hard\n=== 123 456 ===\nplease diagnose this panic and fix cleanly",
			[]string{"please diagnose this panic and fix cleanly"}},
		{"image placeholders excluded",
			"[image: source: /var/folders/rq/xyz/screenshot.png]\nmake the header look like this screenshot",
			[]string{"make the header look like this screenshot"}},
		{"under 4 tokens dropped", "run the tests. yes go ahead now then", []string{"yes go ahead now then"}},
		{"under 4 tokens dropped even when over 16 runes", "recompile everything everywhere", nil},
		{"under 16 runes dropped", "do it all now", nil},
	}
	for _, c := range cases {
		if got := extractClauses(c.turn); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: extractClauses = %#v, want %#v", c.name, got, c.want)
		}
	}
}

func TestNormalizeClauseAndTokens(t *testing.T) {
	norm := normalizeClause("  Leave the Branch   as-is, for MANUAL testing!  ")
	if norm != "leave the branch as-is, for manual testing" {
		t.Fatalf("norm = %q", norm)
	}
	toks := ClauseTokens(norm)
	want := []string{"leave", "the", "branch", "as-is", "for", "manual", "testing"}
	if !reflect.DeepEqual(toks, want) {
		t.Errorf("tokens = %v, want %v", toks, want)
	}
}

func TestExtractClausesSanitizes(t *testing.T) {
	got := extractClauses("look at /Users/dev/Developer/alpha/main.go and fix the bug there")
	if len(got) != 1 || got[0] != "look at [path] and fix the bug there" {
		t.Fatalf("got %#v", got)
	}
}
