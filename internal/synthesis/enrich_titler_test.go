package synthesis

import (
	"context"
	"encoding/json"
	"reflect"
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
