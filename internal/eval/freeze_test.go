package eval

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
	sha1, n1, err := freezeFile(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if n1 != int64(len("same")) {
		t.Fatalf("bytes = %d, want %d", n1, len("same"))
	}
	// identical content re-freezes silently
	sha2, n2, err := freezeFile(src, dst)
	if err != nil {
		t.Fatalf("idempotent re-freeze: %v", err)
	}
	if sha1 != sha2 {
		t.Fatalf("sha mismatch on re-freeze: %s vs %s", sha1, sha2)
	}
	if n2 != int64(len("same")) {
		t.Fatalf("bytes on re-freeze = %d, want %d", n2, len("same"))
	}
	// different content is refused
	src2 := writeTemp(t, dir, "s2.jsonl", "different")
	_, _, err = freezeFile(src2, dst)
	if err == nil || !strings.Contains(err.Error(), "append-only violation") {
		t.Fatalf("want append-only violation, got %v", err)
	}
}

func TestFreezeFileCorruptFixture(t *testing.T) {
	dir := t.TempDir()
	src := writeTemp(t, dir, "s.jsonl", "content")
	dst := filepath.Join(dir, "s.jsonl.gz")
	// pre-populate dst with garbage (non-gzip bytes)
	if err := os.WriteFile(dst, []byte("not valid gzip data"), 0o644); err != nil {
		t.Fatal(err)
	}
	originalContent, _ := os.ReadFile(dst)
	// freezeFile should reject the corrupt fixture and not overwrite it
	_, _, err := freezeFile(src, dst)
	if err == nil {
		t.Fatal("expected error for corrupt fixture, got nil")
	}
	// verify the garbage file was NOT overwritten
	afterContent, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterContent) != string(originalContent) {
		t.Fatalf("corrupt fixture was overwritten: before %q, after %q", originalContent, afterContent)
	}
}
