package synthesis

import (
	"reflect"
	"regexp"
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
