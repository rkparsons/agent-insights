package cmd

import "testing"

func TestParseInsightsArgs(t *testing.T) {
	cases := []struct {
		args      []string
		wantMode  string
		wantForce bool
		wantTh    int
		wantErr   bool
	}{
		{[]string{"analyze", "sess-1"}, "single", false, 5, false},
		{[]string{"analyze", "--backfill"}, "backfill", false, 5, false},
		{[]string{"analyze", "--backfill", "--force", "--threshold", "3"}, "backfill", true, 3, false},
		{[]string{"analyze", "sess-1", "--force"}, "single", true, 5, false},
		{[]string{"analyze"}, "", false, 0, true},
		{[]string{"bogus"}, "", false, 0, true},
		{[]string{"analyze", "--backfill", "sess-1"}, "", false, 0, true},
		{[]string{"analyze", "--bogus-flag"}, "", false, 0, true},
		{[]string{"analyze", "--threshold"}, "", false, 0, true},
		{[]string{"analyze", "--timeout"}, "", false, 0, true},
	}
	for _, c := range cases {
		mode, target, opts, err := parseInsightsArgs(c.args)
		if (err != nil) != c.wantErr {
			t.Errorf("%v: err=%v wantErr=%v", c.args, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if mode != c.wantMode || opts.Force != c.wantForce || opts.MinAssistantTurns != c.wantTh {
			t.Errorf("%v: mode=%q force=%v th=%d target=%q", c.args, mode, opts.Force, opts.MinAssistantTurns, target)
		}
	}
}
