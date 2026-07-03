package synthesis

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"testing"
)

func TestNewestInRepoDir_WarnsOnUnreadableDir(t *testing.T) {
	// A regular file, not a directory: os.ReadDir returns a non-IsNotExist
	// error (ENOTDIR). That must be surfaced to stderr, not silently swallowed
	// as "this repo has no synthesis".
	notDir := filepath.Join(t.TempDir(), "acme")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	if _, ok := newestInRepoDir(notDir); ok {
		t.Fatal("newestInRepoDir on a non-dir returned ok=true")
	}
	if !bytes.Contains(buf.Bytes(), []byte("acme")) {
		t.Errorf("unreadable repo dir was swallowed without a warning; log = %q", buf.String())
	}
}

func writeSynthesisJSON(t *testing.T, root, repo, date string, s RepoSynthesis) {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	dir := filepath.Join(root, "synthesis", repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, date+".json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func writeRaw(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestLoadSyntheses_NewestPerRepo(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", root)
	writeSynthesisJSON(t, root, "client-project", "2026-06-30", RepoSynthesis{Repo: "client-project", Meta: Meta{Model: "old"}})
	writeSynthesisJSON(t, root, "client-project", "2026-07-02", RepoSynthesis{Repo: "client-project", Meta: Meta{Model: "new"}})
	writeSynthesisJSON(t, root, "tmux-ctrl", "2026-07-01", RepoSynthesis{Repo: "tmux-ctrl", Meta: Meta{Model: "t"}})

	got, err := LoadSyntheses()
	if err != nil {
		t.Fatalf("LoadSyntheses: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (one per repo)", len(got))
	}
	if got[0].Repo != "client-project" || got[1].Repo != "tmux-ctrl" {
		t.Fatalf("repos = %q,%q, want client-project,tmux-ctrl (sorted)", got[0].Repo, got[1].Repo)
	}
	if got[0].Meta.Model != "new" {
		t.Errorf("client-project model = %q, want newest (2026-07-02)", got[0].Meta.Model)
	}
}

func TestLoadSyntheses_SkipsMalformed(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", root)
	writeSynthesisJSON(t, root, "client-project", "2026-07-02", RepoSynthesis{Repo: "client-project"})
	// a broken newest file must not blank the section — it's skipped, older wins.
	// Overwrite the 2026-07-02 slot (the newest date) with malformed JSON so it's
	// the only entry at that date; 2026-07-01 is then the newest PARSEABLE file.
	writeSynthesisJSON(t, root, "client-project", "2026-07-01", RepoSynthesis{Repo: "client-project", Meta: Meta{Model: "fallback"}})
	writeRaw(t, filepath.Join(root, "synthesis", "client-project", "2026-07-02.json"), []byte("{not json"))
	got, err := LoadSyntheses()
	if err != nil {
		t.Fatalf("LoadSyntheses: %v", err)
	}
	if len(got) != 1 || got[0].Meta.Model != "fallback" {
		t.Fatalf("got %+v, want the newest PARSEABLE (2026-07-01 fallback)", got)
	}
}

func TestLoadSyntheses_MissingDir(t *testing.T) {
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	got, err := LoadSyntheses()
	if err != nil || got != nil {
		t.Fatalf("got (%v,%v), want (nil,nil) for missing synthesis dir", got, err)
	}
}
