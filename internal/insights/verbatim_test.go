package insights

import "testing"

func TestVerbatimTwoCorporaTwoModes(t *testing.T) {
	in := `{"type":"user","message":{"content":"I always want   tabs not spaces"}}
{"type":"assistant","message":{"id":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"the assistant said FOO_BAR"}]}}`
	v := runExtract(t, in, noRepo).Verbatim

	if !v.ContainsUser("I always want   tabs not spaces") {
		t.Fatal("exact user quote should pass ContainsUser")
	}
	if v.ContainsUser("FOO_BAR") {
		t.Fatal("assistant text must NOT pass ContainsUser")
	}
	if !v.ContainsAny("FOO_BAR") {
		t.Fatal("assistant text should pass ContainsAny")
	}
	if v.ContainsUser("I always want tabs not spaces") {
		t.Fatal("whitespace-collapsed quote must FAIL exact ContainsUser")
	}
	if !v.ContainsUserNormalized("I always want tabs not spaces") {
		t.Fatal("whitespace-collapsed quote should PASS normalized")
	}
	if v.ContainsUser("") || v.ContainsAny("") {
		t.Fatal("empty quote must never match")
	}
}

func TestVerbatimRejectionReasonIsUserAuthored(t *testing.T) {
	in := `{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"The user doesn't want to proceed with this tool use. The tool use was rejected. To tell you how to proceed, the user said: keep the old API"}]}}`
	v := runExtract(t, in, noRepo).Verbatim
	if !v.ContainsUser("keep the old API") {
		t.Fatal("rejection reason should be in the user corpus")
	}
	if v.ContainsUser("doesn't want to proceed") {
		t.Fatal("rejection preamble must not be in the user corpus")
	}
}
