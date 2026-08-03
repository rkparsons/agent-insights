package insightseval

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
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

	m, stats, err := FreezeCorpus(data, byID, frozenAt, insights.Config{})
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

	m1, stats1, err := FreezeCorpus(data, nil, frozenAt1, insights.Config{})
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
	m2, stats2, err := FreezeCorpus(data, nil, frozenAt2, insights.Config{})
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
	if _, _, err := FreezeCorpus(data, nil, frozenAt, insights.Config{}); err != nil {
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

	_, _, err = FreezeCorpus(data, nil, frozenAt, insights.Config{})
	if err == nil || !strings.Contains(err.Error(), "sess-1") {
		t.Fatalf("want hard error naming sess-1, got %v", err)
	}
}

func TestFreezeCorpusRerunFreezesNewSession(t *testing.T) {
	projects, data := freezeCorpusFixture(t)
	frozenAt := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if _, _, err := FreezeCorpus(data, nil, frozenAt, insights.Config{}); err != nil {
		t.Fatal(err)
	}

	proj := filepath.Join(projects, "-Users-x-Developer-myrepo")
	if err := os.WriteFile(filepath.Join(proj, "sess-3.jsonl"), []byte(`{"a":3}`), 0o644); err != nil {
		t.Fatal(err)
	}

	m, stats, err := FreezeCorpus(data, nil, frozenAt, insights.Config{})
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

// TestFreezeCorpusRerunPreservesEntryAfterLiveTranscriptPruned covers finding
// A: a session already frozen must not vanish from the manifest (and get
// reclassified as a gap) just because Claude Code later pruned its live
// transcript out from under a re-freeze.
func TestFreezeCorpusRerunPreservesEntryAfterLiveTranscriptPruned(t *testing.T) {
	projects, data := freezeCorpusFixture(t)
	frozenAt := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	m1, _, err := FreezeCorpus(data, nil, frozenAt, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}

	proj := filepath.Join(projects, "-Users-x-Developer-myrepo")
	if err := os.Remove(filepath.Join(proj, "sess-1.jsonl")); err != nil {
		t.Fatal(err)
	}

	m2, stats2, err := FreezeCorpus(data, nil, frozenAt, insights.Config{})
	if err != nil {
		t.Fatalf("re-freeze after live prune must not error: %v", err)
	}
	if stats2.Frozen != 0 || stats2.AlreadyFrozen != 3 {
		t.Fatalf("stats = %+v, want 0 frozen / 3 already (pruned session still preserved)", stats2)
	}
	if len(m2.Entries) != 2 {
		t.Fatalf("entries = %d, want 2 (sess-1 preserved, sess-2 unchanged)", len(m2.Entries))
	}
	byID1 := map[string]ManifestEntry{}
	for _, e := range m1.Entries {
		byID1[e.SessionID] = e
	}
	found := false
	for _, e := range m2.Entries {
		if e.SessionID != "sess-1" {
			continue
		}
		found = true
		if e != byID1["sess-1"] {
			t.Fatalf("sess-1 entry not preserved verbatim after live prune: %+v vs %+v", e, byID1["sess-1"])
		}
	}
	if !found {
		t.Fatal("sess-1 entry missing from manifest after its live transcript was pruned — reclassified as a gap")
	}
}

// TestFreezeCorpusDedupesSameSessionAcrossProjectDirs covers finding B: a
// resume can copy an entire project dir into a second project dir, surfacing
// the same session-id twice from transcript.WalkTranscripts. FreezeCorpus must
// collapse to one entry (newest content), not duplicate or hard-fail.
func TestFreezeCorpusDedupesSameSessionAcrossProjectDirs(t *testing.T) {
	projects := t.TempDir()
	data := t.TempDir()
	projA := filepath.Join(projects, "-Users-x-Developer-myrepo")
	projB := filepath.Join(projects, "-Users-x-Developer-myrepo-resume")
	if err := os.MkdirAll(projA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)

	older := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	oldPath := filepath.Join(projA, "sess-dup.jsonl")
	newPath := filepath.Join(projB, "sess-dup.jsonl")
	if err := os.WriteFile(oldPath, []byte(`{"a":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte(`{"a":"new-and-different"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldPath, older, older); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, newer, newer); err != nil {
		t.Fatal(err)
	}

	frozenAt := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	m, stats, err := FreezeCorpus(data, nil, frozenAt, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Frozen != 1 {
		t.Fatalf("stats.Frozen = %d, want 1 (deduped to a single entry)", stats.Frozen)
	}
	count := 0
	var got ManifestEntry
	for _, e := range m.Entries {
		if e.SessionID == "sess-dup" {
			count++
			got = e
		}
	}
	if count != 1 {
		t.Fatalf("sess-dup entries = %d, want 1", count)
	}
	if !got.Mtime.Equal(newer) {
		t.Fatalf("Mtime = %v, want newest %v", got.Mtime, newer)
	}
	sha, err := frozenSHA(filepath.Join(data, "corpus", "sess-dup.jsonl.gz"))
	if err != nil {
		t.Fatal(err)
	}
	wantSum := sha256.Sum256([]byte(`{"a":"new-and-different"}`))
	if sha != hex.EncodeToString(wantSum[:]) {
		t.Fatalf("frozen content sha = %s, want newest-content sha", sha)
	}
}

// TestListSidechainsUnreadableSubdirErrors covers finding C: a subtree read
// error (e.g. a permission-denied directory) must propagate, not be
// swallowed into a silent partial freeze.
func TestListSidechainsUnreadableSubdirErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission bits are not enforced")
	}
	projects := t.TempDir()
	blocked := filepath.Join(projects, "-Users-x-Developer-myrepo", "sess-1", "subagents")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "agent-abc.jsonl"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blocked, 0o755) })

	if _, err := listSidechains(projects); err == nil {
		t.Fatal("want error for unreadable subagents dir, got nil")
	}
}

// TestListSidechainsMissingRootReturnsNilError guards the restructured
// root-missing check: a missing projectsDir is not an error.
func TestListSidechainsMissingRootReturnsNilError(t *testing.T) {
	dir := t.TempDir()
	out, err := listSidechains(filepath.Join(dir, "does-not-exist"))
	if err != nil {
		t.Fatalf("missing root must not error: %v", err)
	}
	if out != nil {
		t.Fatalf("out = %v, want nil", out)
	}
}

// TestFreezeCorpusFreezesAgentMetaSidechains covers finding D: any agent-*
// file under a subagents/ dir must be frozen, not just agent-*.jsonl.
func TestFreezeCorpusFreezesAgentMetaSidechains(t *testing.T) {
	projects := t.TempDir()
	data := t.TempDir()
	proj := filepath.Join(projects, "-Users-x-Developer-myrepo")
	subagents := filepath.Join(proj, "sess-1", "subagents")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "sess-1.jsonl"), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subagents, "agent-x.jsonl"), []byte(`{"sub":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subagents, "agent-x.meta.json"), []byte(`{"meta":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)

	m, _, err := FreezeCorpus(data, nil, time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC), insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]bool{}
	for _, sc := range m.Sidechains {
		files[sc.File] = true
	}
	if !files["agent-x.jsonl"] || !files["agent-x.meta.json"] {
		t.Fatalf("sidechains = %v, want both agent-x.jsonl and agent-x.meta.json", files)
	}
	for _, p := range []string{
		filepath.Join(data, "corpus-sidechains", "sess-1", "agent-x.jsonl.gz"),
		filepath.Join(data, "corpus-sidechains", "sess-1", "agent-x.meta.json.gz"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing %s", p)
		}
	}
}

// TestFreezeCorpusDedupesSameSidechainAcrossProjectDirs covers finding E: a
// resume can copy an entire project dir with sidechains into a second project
// dir, surfacing the same parent+filename twice. FreezeCorpus must collapse to
// one entry (newest content), not duplicate or hard-fail.
func TestFreezeCorpusDedupesSameSidechainAcrossProjectDirs(t *testing.T) {
	projects := t.TempDir()
	data := t.TempDir()
	projA := filepath.Join(projects, "-Users-x-Developer-myrepo")
	projB := filepath.Join(projects, "-Users-x-Developer-myrepo-resume")
	if err := os.MkdirAll(filepath.Join(projA, "sess-1", "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projB, "sess-1", "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)

	mustWrite := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(filepath.Join(projA, "sess-1.jsonl"), `{"a":1}`)
	mustWrite(filepath.Join(projB, "sess-1.jsonl"), `{"a":1}`)

	older := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	oldSidechain := filepath.Join(projA, "sess-1", "subagents", "agent-x.jsonl")
	newSidechain := filepath.Join(projB, "sess-1", "subagents", "agent-x.jsonl")
	mustWrite(oldSidechain, `{"sub":"old"}`)
	mustWrite(newSidechain, `{"sub":"new-and-different"}`)
	if err := os.Chtimes(oldSidechain, older, older); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newSidechain, newer, newer); err != nil {
		t.Fatal(err)
	}

	frozenAt := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	m, _, err := FreezeCorpus(data, nil, frozenAt, insights.Config{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	var got SidechainEntry
	for _, sc := range m.Sidechains {
		if sc.ParentSessionID == "sess-1" && sc.File == "agent-x.jsonl" {
			count++
			got = sc
		}
	}
	if count != 1 {
		t.Fatalf("agent-x.jsonl sidechain entries = %d, want 1 (deduped)", count)
	}
	if !got.Mtime.Equal(newer) {
		t.Fatalf("Mtime = %v, want newest %v", got.Mtime, newer)
	}
	sha, err := frozenSHA(filepath.Join(data, "corpus-sidechains", "sess-1", "agent-x.jsonl.gz"))
	if err != nil {
		t.Fatal(err)
	}
	wantSum := sha256.Sum256([]byte(`{"sub":"new-and-different"}`))
	if sha != hex.EncodeToString(wantSum[:]) {
		t.Fatalf("frozen content sha = %s, want newest-content sha", sha)
	}
}
