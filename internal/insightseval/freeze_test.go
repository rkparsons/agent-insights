package insightseval

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFreezeFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := writeTemp(t, dir, "s.jsonl", `{"type":"user"}`+"\n")
	dst := filepath.Join(dir, "out", "s.jsonl.gz")

	sha1, n, err := freezeFile(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(`{"type":"user"}`)+1) {
		t.Fatalf("bytes = %d", n)
	}
	f, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"type":"user"}`+"\n" {
		t.Fatalf("decompressed = %q", raw)
	}
	sha2, err := frozenSHA(dst)
	if err != nil {
		t.Fatal(err)
	}
	if sha1 != sha2 {
		t.Fatalf("sha mismatch: %s vs %s", sha1, sha2)
	}
}

func TestFreezeFileAppendOnly(t *testing.T) {
	dir := t.TempDir()
	src := writeTemp(t, dir, "s.jsonl", "same")
	dst := filepath.Join(dir, "s.jsonl.gz")
	if _, _, err := freezeFile(src, dst); err != nil {
		t.Fatal(err)
	}
	// identical content re-freezes silently
	if _, _, err := freezeFile(src, dst); err != nil {
		t.Fatalf("idempotent re-freeze: %v", err)
	}
	// different content is refused
	src2 := writeTemp(t, dir, "s2.jsonl", "different")
	_, _, err := freezeFile(src2, dst)
	if err == nil || !strings.Contains(err.Error(), "append-only violation") {
		t.Fatalf("want append-only violation, got %v", err)
	}
}
