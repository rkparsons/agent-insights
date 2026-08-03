package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildCorpusFixture freezes two sessions + one sidechain pair into a data dir
// and writes the matching manifest.json, returning (dataDir, plainDir).
func buildCorpusFixture(t *testing.T) (string, string) {
	t.Helper()
	src, data := t.TempDir(), t.TempDir()
	mt := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	m := Manifest{FrozenAt: mt}
	for _, id := range []string{"s1", "s2"} {
		p := writeTemp(t, src, id+".jsonl", `{"type":"user","cwd":"/x/`+id+`"}`+"\n")
		sha, n, err := freezeFile(p, filepath.Join(data, "corpus", id+".jsonl.gz"))
		if err != nil {
			t.Fatal(err)
		}
		m.Entries = append(m.Entries, ManifestEntry{SessionID: id, SHA256: sha, Mtime: mt, Bytes: n})
	}
	sc := writeTemp(t, src, "agent-a.jsonl", `{"sub":1}`)
	scSHA, scN, err := freezeFile(sc, filepath.Join(data, "corpus-sidechains", "s1", "agent-a.jsonl.gz"))
	if err != nil {
		t.Fatal(err)
	}
	scMeta := writeTemp(t, src, "agent-a.meta.json", `{"meta":1}`)
	metaSHA, metaN, err := freezeFile(scMeta, filepath.Join(data, "corpus-sidechains", "s1", "agent-a.meta.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	m.Sidechains = []SidechainEntry{
		{ParentSessionID: "s1", File: "agent-a.jsonl", SHA256: scSHA, Mtime: mt, Bytes: scN},
		{ParentSessionID: "s1", File: "agent-a.meta.json", SHA256: metaSHA, Mtime: mt, Bytes: metaN},
	}
	if err := writeJSON(filepath.Join(data, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	return data, t.TempDir()
}

func TestCorpusRefMaterializesAndVerifies(t *testing.T) {
	data, plain := buildCorpusFixture(t)
	c, err := OpenCorpus(data, plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.ManifestHash()) != 64 {
		t.Fatalf("manifest hash: %q", c.ManifestHash())
	}
	if !c.Has("s1") || c.Has("missing") {
		t.Fatal("Has is wrong")
	}
	ref, err := c.Ref("s1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(ref.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"cwd":"/x/s1"`) {
		t.Fatalf("materialized content: %q", raw)
	}
	if ref.SessionID != "s1" || !ref.Mtime.Equal(time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("ref: %+v", ref)
	}
	// second Ref reuses the plain file (still verified)
	if _, err := c.Ref("s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Ref("missing"); err == nil || !strings.Contains(err.Error(), "no frozen transcript") {
		t.Fatalf("gap error: %v", err)
	}
}

func TestCorpusRefRejectsTamperedPlainFile(t *testing.T) {
	data, plain := buildCorpusFixture(t)
	c, err := OpenCorpus(data, plain)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := c.Ref("s1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ref.Path, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Ref("s1"); err == nil || !strings.Contains(err.Error(), "does not match manifest sha") {
		t.Fatalf("tamper check: %v", err)
	}
}

func TestCorpusSidechainRefsJSONLOnly(t *testing.T) {
	data, plain := buildCorpusFixture(t)
	c, err := OpenCorpus(data, plain)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := c.SidechainRefs("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || filepath.Base(refs[0].Path) != "agent-a.jsonl" {
		t.Fatalf("sidechain refs: %+v", refs)
	}
	if got := c.SidechainFiles("s1"); len(got) != 2 {
		t.Fatalf("sidechain files: %+v", got)
	}
	if refs, _ := c.SidechainRefs("s2"); len(refs) != 0 {
		t.Fatalf("s2 has no sidechains: %+v", refs)
	}
}
