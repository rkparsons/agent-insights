package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeProject(t *testing.T, root, project, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const oneLine = `{"type":"user","cwd":"/x","message":{"content":"hi there friend"}}`

func TestWalkTranscriptsSkipsSubagentsAndNonSessions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENT_INSIGHTS_PROJECTS_DIR", root)
	writeProject(t, root, "proj-a", "11111111-1111-1111-1111-111111111111.jsonl", oneLine)
	writeProject(t, root, "proj-a/subagents", "agent-22222222.jsonl", oneLine)
	writeProject(t, root, "proj-a", "notes.txt", "ignore me")

	refs, err := WalkTranscripts()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("want 1 ref (subagents/ + .txt excluded), got %d: %+v", len(refs), refs)
	}
	if refs[0].SessionID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("session id wrong: %q", refs[0].SessionID)
	}
}

func TestFindTranscriptZeroAndPicksNewest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENT_INSIGHTS_PROJECTS_DIR", root)
	if _, err := FindTranscript("missing"); err == nil {
		t.Error("want error for 0 matches")
	}
	id := "33333333-3333-3333-3333-333333333333"
	old := writeProject(t, root, "proj-a", id+".jsonl", oneLine)
	newer := writeProject(t, root, "proj-b", id+".jsonl", oneLine)
	// make proj-a strictly older than proj-b
	t0 := mustStat(t, old).ModTime()
	if err := os.Chtimes(old, t0, t0.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	ref, err := FindTranscript(id)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Path != newer {
		t.Errorf("want newest %q, got %q", newer, ref.Path)
	}
}

func mustStat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func TestLoadTranscriptReturnsMtime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENT_INSIGHTS_PROJECTS_DIR", root)
	p := writeProject(t, root, "proj-a", "44444444.jsonl", oneLine)
	ev, _, mtime, err := LoadTranscript(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) == 0 {
		t.Error("want decoded events")
	}
	if mtime != mustStat(t, p).ModTime() {
		t.Errorf("mtime mismatch")
	}
}
