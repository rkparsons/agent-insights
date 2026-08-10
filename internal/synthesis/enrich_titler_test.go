package synthesis

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

func TestTitlerFromRunnerParsesTitles(t *testing.T) {
	var captured []byte
	run := func(ctx context.Context, stdin []byte) ([]byte, error) {
		captured = stdin
		return []byte(`{"is_error":false,"result":"","structured_output":{"titles":[{"index":0,"title":"Do the thing"},{"index":2,"title":"Other"}]}}`), nil
	}
	titler := titlerFromRunner(run)
	got, err := titler(context.Background(), []TitleReq{
		{Index: 0, Type: "habit", Statement: "a"},
		{Index: 2, Type: "hook", Statement: "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]string{0: "Do the thing", 2: "Other"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("titles = %v, want %v", got, want)
	}
	var reqs []TitleReq
	if err := json.Unmarshal(captured, &reqs); err != nil {
		t.Fatalf("stdin not a TitleReq array: %v (%s)", err, captured)
	}
	if len(reqs) != 2 || reqs[1].Statement != "b" {
		t.Errorf("stdin payload = %+v", reqs)
	}
}

func TestTitlerFromRunnerErrors(t *testing.T) {
	cases := map[string]string{
		"is_error":     `{"is_error":true,"result":"nope","structured_output":null}`,
		"null output":  `{"is_error":false,"result":"","structured_output":null}`,
		"bad envelope": `not json`,
	}
	for name, out := range cases {
		titler := titlerFromRunner(func(ctx context.Context, stdin []byte) ([]byte, error) {
			return []byte(out), nil
		})
		if _, err := titler(context.Background(), []TitleReq{{Index: 0, Statement: "s"}}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// A model that answers with array positions instead of the requested indices
// must never silently attach a title to the wrong recommendation.
func TestTitlerFromRunnerRejectsIndexMismatch(t *testing.T) {
	run := func(ctx context.Context, stdin []byte) ([]byte, error) {
		return []byte(`{"is_error":false,"result":"","structured_output":{"titles":[{"index":0,"title":"a"},{"index":1,"title":"b"},{"index":2,"title":"c"}]}}`), nil
	}
	titler := titlerFromRunner(run)
	_, err := titler(context.Background(), []TitleReq{
		{Index: 1, Statement: "x"},
		{Index: 3, Statement: "y"},
		{Index: 5, Statement: "z"},
	})
	if err == nil {
		t.Fatal("expected error on index-set mismatch (response used array positions 0,1,2, not requested indices 1,3,5)")
	}
}

func TestTitlerFromRunnerRejectsDuplicateIndex(t *testing.T) {
	run := func(ctx context.Context, stdin []byte) ([]byte, error) {
		return []byte(`{"is_error":false,"result":"","structured_output":{"titles":[{"index":0,"title":"a"},{"index":0,"title":"b"}]}}`), nil
	}
	titler := titlerFromRunner(run)
	if _, err := titler(context.Background(), []TitleReq{{Index: 0, Statement: "x"}}); err == nil {
		t.Fatal("expected error on duplicate index")
	}
}

func TestTitlerFromRunnerRejectsMissingIndex(t *testing.T) {
	run := func(ctx context.Context, stdin []byte) ([]byte, error) {
		return []byte(`{"is_error":false,"result":"","structured_output":{"titles":[{"index":0,"title":"a"}]}}`), nil
	}
	titler := titlerFromRunner(run)
	if _, err := titler(context.Background(), []TitleReq{{Index: 0, Statement: "x"}, {Index: 1, Statement: "y"}}); err == nil {
		t.Fatal("expected error on missing index (requested 0,1, got only 0)")
	}
}

func TestNewTitleCommandPinsConfigDirAndCwd(t *testing.T) {
	cmd, err := newTitleCommand(context.Background(), nil, "/tmp/cfg", "/tmp/work")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Dir != "/tmp/work" {
		t.Fatalf("Dir = %q", cmd.Dir)
	}
	if !slices.Contains(cmd.Env, "CLAUDE_CONFIG_DIR=/tmp/cfg") {
		t.Fatal("env missing pinned CLAUDE_CONFIG_DIR")
	}
	// An unpinned config dir stays inherited (production); the workdir never can.
	inherited, err := newTitleCommand(context.Background(), nil, "", "/tmp/work")
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range inherited.Env {
		if kv == "CLAUDE_CONFIG_DIR=" {
			t.Fatal("unpinned command must not append an empty CLAUDE_CONFIG_DIR")
		}
	}
}

// The nested claude resolves project config from its cwd, so an empty workDir
// is a wiring bug that must fail loudly rather than run against whatever
// config is ambient in the caller's cwd.
func TestNewTitleCommandRejectsEmptyWorkDir(t *testing.T) {
	if _, err := newTitleCommand(context.Background(), nil, "/tmp/cfg", ""); err == nil {
		t.Fatal("expected an error for an empty workDir")
	}
	titler := NewClaudeTitler("")
	if _, err := titler(context.Background(), []TitleReq{{Index: 0, Statement: "s"}}); err == nil {
		t.Fatal("expected the titler to refuse to run without a workdir")
	}
}
