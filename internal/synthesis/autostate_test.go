package synthesis

import (
	"strings"
	"testing"
)

func TestDeriveAutoState(t *testing.T) {
	due := []string{"r1"}
	cases := []struct {
		name     string
		rs       RunState
		has      bool
		lockHeld bool
		due      []string
		want     AutoStatus
	}{
		{"idle", RunState{}, false, false, nil, AutoIdle},
		{"due", RunState{}, false, false, due, AutoDue},
		{"running", RunState{Status: "running"}, true, true, due, AutoRunning},
		{"crashed is error", RunState{Status: "running"}, true, false, due, AutoError},
		{"failed sticky over due", RunState{Status: "failed", Reason: "boom"}, true, false, due, AutoError},
		{"ok falls through to due", RunState{Status: "ok"}, true, false, due, AutoDue},
		{"ok and nothing due", RunState{Status: "ok"}, true, false, nil, AutoIdle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveAutoState(tc.rs, tc.has, tc.lockHeld, tc.due)
			if got.Status != tc.want {
				t.Fatalf("got %v want %v", got.Status, tc.want)
			}
		})
	}
}

func TestDeriveAutoState_FailedLastOutcomeMentionsReason(t *testing.T) {
	got := DeriveAutoState(RunState{Status: "failed", Reason: "boom"}, true, false, []string{"r1"})
	if got.LastOutcome == "" {
		t.Fatal("LastOutcome must be non-empty for a failed run")
	}
	if !strings.Contains(got.LastOutcome, "boom") {
		t.Errorf("LastOutcome = %q, want it to mention the reason %q", got.LastOutcome, "boom")
	}
}
