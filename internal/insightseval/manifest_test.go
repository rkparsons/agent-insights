package insightseval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tmux-ctrl/internal/insights"
)

func TestFreezeCorpusWritesManifestAndFiles(t *testing.T) {
	projects := t.TempDir()
	data := t.TempDir()
	proj := filepath.Join(projects, "-Users-x-Developer-myrepo")
	if err := os.MkdirAll(filepath.Join(proj, "sess-1", "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(filepath.Join(proj, "sess-1.jsonl"), `{"a":1}`)
	mustWrite(filepath.Join(proj, "sess-2.jsonl"), `{"a":2}`)
	mustWrite(filepath.Join(proj, "sess-1", "subagents", "agent-abc.jsonl"), `{"sub":1}`)
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)

	start := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	byID := map[string]insights.AgentSessionAnalysis{
		"sess-1": {Stats: insights.AgentSessionStats{
			SessionID: "sess-1", Repo: "/Users/x/Developer/myrepo", Start: start,
		}},
	}
	frozenAt := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	m, err := FreezeCorpus(data, byID, frozenAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 2 {
		t.Fatalf("entries = %d", len(m.Entries))
	}
	// sorted by session-id
	if m.Entries[0].SessionID != "sess-1" || m.Entries[1].SessionID != "sess-2" {
		t.Fatalf("order: %s, %s", m.Entries[0].SessionID, m.Entries[1].SessionID)
	}
	if m.Entries[0].RepoKey != "myrepo" || !m.Entries[0].Start.Equal(start) {
		t.Fatalf("pool join: %+v", m.Entries[0])
	}
	if m.Entries[1].RepoKey != "" {
		t.Fatalf("unmatched session got repo key %q", m.Entries[1].RepoKey)
	}
	if len(m.Sidechains) != 1 || m.Sidechains[0].ParentSessionID != "sess-1" || m.Sidechains[0].File != "agent-abc.jsonl" {
		t.Fatalf("sidechains: %+v", m.Sidechains)
	}
	for _, p := range []string{
		filepath.Join(data, "corpus", "sess-1.jsonl.gz"),
		filepath.Join(data, "corpus", "sess-2.jsonl.gz"),
		filepath.Join(data, "corpus-sidechains", "sess-1", "agent-abc.jsonl.gz"),
		filepath.Join(data, "manifest.json"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing %s: %v", p, err)
		}
	}
	var onDisk Manifest
	raw, err := os.ReadFile(filepath.Join(data, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if !onDisk.FrozenAt.Equal(frozenAt) || len(onDisk.Entries) != 2 {
		t.Fatalf("manifest on disk: %+v", onDisk)
	}
}
