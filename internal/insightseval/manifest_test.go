package insightseval

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

	m, stats, err := FreezeCorpus(data, byID, frozenAt)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Frozen != 3 || stats.AlreadyFrozen != 0 || stats.Diverged != 0 {
		t.Fatalf("stats = %+v, want 3 frozen / 0 already / 0 diverged", stats)
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

// freezeCorpusFixture builds a projects tree with sess-1/sess-2 transcripts
// and one sidechain, wired through TMUX_CTRL_CLAUDE_PROJECTS_DIR, and returns
// the projects dir plus a fresh data dir.
func freezeCorpusFixture(t *testing.T) (projects, data string) {
	t.Helper()
	projects, data = t.TempDir(), t.TempDir()
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
	return projects, data
}

func TestFreezeCorpusRerunPreservesEntriesAndTracksDivergence(t *testing.T) {
	projects, data := freezeCorpusFixture(t)
	frozenAt1 := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	m1, stats1, err := FreezeCorpus(data, nil, frozenAt1)
	if err != nil {
		t.Fatal(err)
	}
	if stats1.Frozen != 3 || stats1.AlreadyFrozen != 0 || stats1.Diverged != 0 {
		t.Fatalf("first-freeze stats = %+v", stats1)
	}

	// live transcript keeps growing after the first freeze
	proj := filepath.Join(projects, "-Users-x-Developer-myrepo")
	f, err := os.OpenFile(filepath.Join(proj, "sess-1.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n{\"a\":3}"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	frozenAt2 := time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)
	m2, stats2, err := FreezeCorpus(data, nil, frozenAt2)
	if err != nil {
		t.Fatalf("re-freeze must not error on append-only-violated live transcript: %v", err)
	}
	if !m2.FrozenAt.Equal(frozenAt1) {
		t.Fatalf("FrozenAt = %v, want original %v preserved", m2.FrozenAt, frozenAt1)
	}
	if stats2.Frozen != 0 || stats2.AlreadyFrozen != 3 || stats2.Diverged != 1 {
		t.Fatalf("re-freeze stats = %+v, want 0 frozen / 3 already / 1 diverged", stats2)
	}
	// entries preserved byte-for-byte against the first freeze
	byID1 := map[string]ManifestEntry{}
	for _, e := range m1.Entries {
		byID1[e.SessionID] = e
	}
	for _, e := range m2.Entries {
		if e != byID1[e.SessionID] {
			t.Fatalf("entry %s not preserved verbatim: %+v vs %+v", e.SessionID, e, byID1[e.SessionID])
		}
	}
	if len(m2.Sidechains) != 1 || m2.Sidechains[0] != m1.Sidechains[0] {
		t.Fatalf("sidechain not preserved verbatim: %+v vs %+v", m2.Sidechains, m1.Sidechains)
	}
	// the frozen corpus file itself is untouched (still the pre-append content)
	sha, err := frozenSHA(filepath.Join(data, "corpus", "sess-1.jsonl.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if sha != byID1["sess-1"].SHA256 {
		t.Fatalf("frozen file content changed on re-run: %s vs %s", sha, byID1["sess-1"].SHA256)
	}
}

func TestFreezeCorpusRerunDetectsTamperedFrozenFile(t *testing.T) {
	_, data := freezeCorpusFixture(t)
	frozenAt := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if _, _, err := FreezeCorpus(data, nil, frozenAt); err != nil {
		t.Fatal(err)
	}

	// simulate out-of-band tampering: overwrite with a *different*, still
	// validly-gzipped, payload
	dst := filepath.Join(data, "corpus", "sess-1.jsonl.gz")
	tf, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(tf)
	if _, err := zw.Write([]byte(`{"tampered":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	tf.Close()

	_, _, err = FreezeCorpus(data, nil, frozenAt)
	if err == nil || !strings.Contains(err.Error(), "sess-1") {
		t.Fatalf("want hard error naming sess-1, got %v", err)
	}
}

func TestFreezeCorpusRerunFreezesNewSession(t *testing.T) {
	projects, data := freezeCorpusFixture(t)
	frozenAt := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if _, _, err := FreezeCorpus(data, nil, frozenAt); err != nil {
		t.Fatal(err)
	}

	proj := filepath.Join(projects, "-Users-x-Developer-myrepo")
	if err := os.WriteFile(filepath.Join(proj, "sess-3.jsonl"), []byte(`{"a":3}`), 0o644); err != nil {
		t.Fatal(err)
	}

	m, stats, err := FreezeCorpus(data, nil, frozenAt)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Frozen != 1 || stats.AlreadyFrozen != 3 {
		t.Fatalf("stats = %+v, want 1 frozen (sess-3) / 3 already", stats)
	}
	ids := map[string]bool{}
	for _, e := range m.Entries {
		ids[e.SessionID] = true
	}
	if !ids["sess-1"] || !ids["sess-2"] || !ids["sess-3"] {
		t.Fatalf("entries missing new or old session: %+v", m.Entries)
	}
	if _, err := os.Stat(filepath.Join(data, "corpus", "sess-3.jsonl.gz")); err != nil {
		t.Fatalf("new session not frozen: %v", err)
	}
}
