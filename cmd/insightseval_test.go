package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"tmux-ctrl/internal/insightseval"
)

func TestSortedBucketKeys(t *testing.T) {
	buckets := map[string]insightseval.BucketPopulations{
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
