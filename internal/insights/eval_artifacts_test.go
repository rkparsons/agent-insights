package insights

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactRunsBasenamesRepoPath(t *testing.T) {
	// resolveRepo returns the repo's full PATH (producer design); the committed
	// artifact must carry only the basename, never the home path (F7 privacy).
	runs := []sessionRun{
		{Stats: AgentSessionStats{Repo: "/Users/dev/Developer/personal/home-cluster"}, Repeats: []RepeatResult{repeat(jf("fully_achieved"))}},
		{Stats: AgentSessionStats{Repo: ""}, Repeats: []RepeatResult{repeat(jf("fully_achieved"))}},
	}
	got := redactRuns(runs)
	if got[0].Repo != "home-cluster" {
		t.Errorf("repo = %q, want basename %q", got[0].Repo, "home-cluster")
	}
	if got[1].Repo != "" {
		t.Errorf("empty repo must stay empty, got %q", got[1].Repo)
	}
	blob, _ := json.Marshal(got)
	if strings.Contains(string(blob), "/Users/") {
		t.Errorf("redacted analyses leak a home path: %s", blob)
	}
}

func TestWriteEvalArtifactsRedacts(t *testing.T) {
	const secretID = "aaaa1111-bbbb-2222-cccc-333344445555"
	inc := FrictionIncident{Type: "wrong_approach", OneLine: "did wrong", EvidenceQuote: "a verbatim secret quote here"}
	sr := sessionRun{
		Stats: AgentSessionStats{
			SessionID: secretID, Cwd: "/secret/alpha", GitBranch: "secret-branch",
			AiTitle: "A Title", Repo: "alpha", AssistantTurns: 12,
			FilesTouched: []string{"/secret/file.go"}, UserTurnFingerprints: []string{"fp-secret"},
		},
		Cell: "zero-extra", ZeroFriction: true, FirstUserTurn: "do the thing",
		Repeats: []RepeatResult{repeat(jf("fully_achieved", inc))},
	}
	rep := assembleReport([]sessionRun{sr})

	dir := t.TempDir()
	local := filepath.Join(t.TempDir(), "manifest.json")
	if err := writeEvalArtifacts(dir, local, rep, []sessionRun{sr}); err != nil {
		t.Fatal(err)
	}

	analyses, err := os.ReadFile(filepath.Join(dir, "light-eval-analyses.json"))
	if err != nil {
		t.Fatal(err)
	}
	cards, err := os.ReadFile(filepath.Join(dir, "light-eval-cards.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "light-eval-report.json")); err != nil {
		t.Fatalf("report missing: %v", err)
	}

	for _, forbidden := range []string{secretID, "/secret/alpha", "secret-branch", "/secret/file.go", "fp-secret", "a verbatim secret quote here"} {
		if strings.Contains(string(analyses), forbidden) {
			t.Errorf("committed analyses leak %q", forbidden)
		}
	}
	if strings.Contains(string(cards), secretID) {
		t.Error("cards leak the session-id")
	}
	// The local manifest is allowed to carry the real id (it is not committed).
	manifest, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), secretID) {
		t.Error("local manifest should retain the real id for reproducibility")
	}
	// Hashing is stable.
	h1 := hashSessionID(secretID)
	h2 := hashSessionID(secretID)
	if h1 != h2 {
		t.Error("hash unstable")
	}
}
