package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rkparsons/agent-insights/internal/eval"
)

func TestSortedBucketKeys(t *testing.T) {
	buckets := map[string]eval.BucketPopulations{
		"zeta": {}, "alpha": {}, "mid": {},
	}
	got := sortedBucketKeys(buckets)
	if !slices.Equal(got, []string{"alpha", "mid", "zeta"}) {
		t.Fatalf("sortedBucketKeys = %v, want sorted order", got)
	}
	if got := sortedBucketKeys(nil); len(got) != 0 {
		t.Fatalf("sortedBucketKeys(nil) = %v, want empty", got)
	}
}

func TestPoolNotWrittenMessage(t *testing.T) {
	if got := poolNotWrittenMessage(false); got != "ISSUES FOUND — baseline-pool/v1 NOT written; resolve and re-run" {
		t.Fatalf("v1 absent message = %q", got)
	}
	got := poolNotWrittenMessage(true)
	if got == "ISSUES FOUND — baseline-pool/v1 NOT written; resolve and re-run" {
		t.Fatal("must not claim NOT written when v1 already exists")
	}
}

func TestDirExists(t *testing.T) {
	dir := t.TempDir()
	if dirExists(filepath.Join(dir, "missing")) {
		t.Fatal("missing dir must report false")
	}
	if !dirExists(dir) {
		t.Fatal("existing dir must report true")
	}
	f := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirExists(f) {
		t.Fatal("a regular file must not report true")
	}
}

func TestParseOutcomeArgs(t *testing.T) {
	opts, err := parseOutcomeArgs([]string{"--scope", "full", "--samples", "5", "--population", "as_consumed", "--l1-sample", "--data", "/d", "--cache", "/c"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Scope != "full" || opts.Samples != 5 || opts.Population != "as_consumed" ||
		!opts.L1Sample || opts.DataDir != "/d" || opts.CacheDir != "/c" {
		t.Fatalf("opts: %+v", opts)
	}
	if _, err := parseOutcomeArgs([]string{"--samples", "zero"}); err == nil {
		t.Fatal("bad --samples must error")
	}
	if _, err := parseOutcomeArgs([]string{"--bogus"}); err == nil {
		t.Fatal("unknown flag must error")
	}
	defaults, err := parseOutcomeArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.DataDir == "" || defaults.CacheDir == "" {
		t.Fatalf("defaults: %+v", defaults)
	}
	if !strings.HasSuffix(defaults.DataDir, "insights-eval-data") {
		t.Fatalf("default data dir: %q", defaults.DataDir)
	}
}

func TestPoolSummaryMessage(t *testing.T) {
	if got := poolSummaryMessage(true, 0); !strings.Contains(got, "retained") {
		t.Fatalf("retained wording: %q", got)
	}
	if got := poolSummaryMessage(false, 296); !strings.Contains(got, "296") {
		t.Fatalf("written wording: %q", got)
	}
}

func TestParseScoreArgs(t *testing.T) {
	opts, err := parseScoreArgs([]string{"--record", "/tmp/r.json", "--repeats", "5", "--data", "/d", "--cache", "/c"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.RecordPath != "/tmp/r.json" || opts.Repeats != 5 || opts.DataDir != "/d" || opts.CacheDir != "/c" {
		t.Fatalf("opts: %+v", opts)
	}
	if _, err := parseScoreArgs([]string{"--bogus"}); err == nil {
		t.Fatal("unknown flag must error")
	}
	if _, err := parseScoreArgs([]string{"--repeats"}); err == nil {
		t.Fatal("missing value must error")
	}

	dev, err := parseScoreArgs([]string{"--targets", "C-02,C-05", "--samples", "1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(dev.Targets) != 2 || dev.Targets[0] != "C-02" || dev.Targets[1] != "C-05" || dev.MaxSamples != 1 {
		t.Fatalf("dev opts: %+v", dev)
	}
	if _, err := parseScoreArgs([]string{"--samples", "1"}); err == nil {
		t.Fatal("--samples without --targets must error: the committed sweep always scores all samples")
	}
	if _, err := parseScoreArgs([]string{"--targets", ""}); err == nil {
		t.Fatal("empty --targets must error")
	}
}
