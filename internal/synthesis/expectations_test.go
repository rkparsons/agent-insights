package synthesis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// realGateExpectation is the private data repo's record of which real repo the
// SYNTHESIS_REAL gate runs against. Real repo names live in
// insights-eval-data/expectations.json, never in this tree.
type realGateExpectation struct {
	Bucket   string `json:"bucket"`    // RepoKey the analyses group under
	RepoPath string `json:"repo_path"` // checkout the adoption checker reads
}

// evalDataDir is the private data repo: INSIGHTS_EVAL_DATA, else the
// ~/Developer/insights-eval-data default the eval commands use.
func evalDataDir(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("INSIGHTS_EVAL_DATA"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, "Developer", "insights-eval-data")
}

// loadRealGateExpectation reads expectations.json from the private data repo.
// A missing or incomplete file fails rather than skips: callers reach it only
// after the operator opted into a spending run, where a silent skip would read
// as a pass.
func loadRealGateExpectation(t *testing.T) realGateExpectation {
	t.Helper()
	return parseRealGateExpectation(t, filepath.Join(evalDataDir(t), "expectations.json"))
}

func parseRealGateExpectation(t *testing.T, path string) realGateExpectation {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("real-gate expectations: %v", err)
	}
	var doc struct {
		RealGate realGateExpectation `json:"real_gate"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if doc.RealGate.Bucket == "" || doc.RealGate.RepoPath == "" {
		t.Fatalf("%s: real_gate needs both bucket and repo_path, got %+v", path, doc.RealGate)
	}
	return doc.RealGate
}

func TestRealGateExpectationParses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "expectations.json")
	body := `{"real_gate":{"bucket":"alpha","repo_path":"/tmp/alpha"}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := parseRealGateExpectation(t, path)
	if got.Bucket != "alpha" || got.RepoPath != "/tmp/alpha" {
		t.Fatalf("expectation = %+v", got)
	}
}

// TestRealGateExpectationResolvesHere surfaces a stale or missing expectation
// for free, instead of at the top of a spending SYNTHESIS_REAL run. Skips
// where the private data repo is absent.
func TestRealGateExpectationResolvesHere(t *testing.T) {
	if _, err := os.Stat(filepath.Join(evalDataDir(t), "expectations.json")); err != nil {
		t.Skip("insights-eval-data expectations.json not present")
	}
	exp := loadRealGateExpectation(t)
	fi, err := os.Stat(exp.RepoPath)
	if err != nil || !fi.IsDir() {
		t.Fatalf("real_gate.repo_path is not a directory: %v", err)
	}
	// The gate groups with a zero insights.Config, so RepoKey reduces to
	// basename(repo) with no alias fold: bucket and repo_path basename cannot
	// disagree without the gate silently grouping into an empty bucket.
	if base := filepath.Base(exp.RepoPath); base != exp.Bucket {
		t.Fatalf("real_gate.bucket %q != basename(repo_path) %q; the gate would find no analyses", exp.Bucket, base)
	}
}
