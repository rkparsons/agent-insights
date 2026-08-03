package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestMaterializeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := Materialize(dir); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	want := map[string][]byte{}
	if err := fs.WalkDir(FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, readErr := FS.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		want[p] = data
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("embedded FS has no files")
	}

	root := filepath.Join(dir, ".claude", "skills")
	got := map[string][]byte{}
	if err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		got[filepath.ToSlash(rel)] = data
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if len(got) != len(want) {
		t.Fatalf("materialized %d files, embedded %d", len(got), len(want))
	}
	for rel, w := range want {
		g, ok := got[rel]
		if !ok {
			t.Errorf("%s not materialized", rel)
			continue
		}
		if string(g) != string(w) {
			t.Errorf("%s bytes differ from the embedded copy", rel)
		}
	}
	// The layout is the contract: the nested claude resolves /<name> from its cwd.
	for _, name := range Names() {
		if _, err := os.Stat(filepath.Join(root, name, "SKILL.md")); err != nil {
			t.Errorf("expected %s/SKILL.md under <dir>/.claude/skills: %v", name, err)
		}
	}
}

func TestSchemasNonEmpty(t *testing.T) {
	for _, c := range []struct {
		name string
		got  []byte
	}{
		{"analysis", AnalysisSchema()},
		{"synthesis", SynthesisSchema()},
	} {
		if len(c.got) == 0 {
			t.Errorf("%s schema is empty", c.name)
			continue
		}
		var v any
		if err := json.Unmarshal(c.got, &v); err != nil {
			t.Errorf("%s schema is not valid JSON: %v", c.name, err)
		}
	}
}

func TestTreeHashStable(t *testing.T) {
	first, second := TreeHash(), TreeHash()
	if first != second {
		t.Fatalf("TreeHash not stable: %s != %s", first, second)
	}
	if first == "" {
		t.Fatal("TreeHash is empty")
	}

	// Equivalence with eval's hashTree (sorted rel path + size + bytes):
	// a hash taken from the embedded tree must equal one taken from a
	// materialized copy, or the eval's skill-hash cache keys shift.
	dir := t.TempDir()
	if err := Materialize(dir); err != nil {
		t.Fatal(err)
	}
	if got := hashDirLikeEval(t, filepath.Join(dir, ".claude", "skills")); got != first {
		t.Fatalf("TreeHash = %s, hash of materialized tree = %s", first, got)
	}
}

// hashDirLikeEval reimplements eval.hashTree's byte stream so the test
// pins the algorithm rather than the implementation.
func hashDirLikeEval(t *testing.T, root string) string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		files[rel] = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	h := sha256.New()
	for _, rel := range rels {
		data, err := os.ReadFile(files[rel])
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestTempWorkdirMaterializesAndCleansUp(t *testing.T) {
	dir, cleanup, err := TempWorkdir()
	if err != nil {
		t.Fatalf("TempWorkdir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills", AnalysisSkill, "SKILL.md")); err != nil {
		t.Fatalf("skills not materialized into the workdir: %v", err)
	}
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("cleanup left %s behind (err=%v)", dir, err)
	}
}
