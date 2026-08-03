package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleAdj(decision string) Adjudication {
	k := AdjKey{TargetID: "C-04", Statement: normalizeStatement("  Match  PROCESS weight\nto task "),
		IDSetHash: idSetHash([]string{"b", "a", "b"}), RubricHash: "rh1", Trigger: "anchor_mismatch"}
	return Adjudication{Key: k, KeyHash: k.Hash(), Decision: decision,
		Note: "confirmed real", DecidedAt: time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)}
}

func TestAdjKeyNormalizationAndHash(t *testing.T) {
	a := sampleAdj("accept")
	if a.Key.Statement != "match process weight to task" {
		t.Fatalf("normalization: %q", a.Key.Statement)
	}
	if idSetHash([]string{"a", "b"}) != a.Key.IDSetHash {
		t.Fatal("id set hash must be order/duplicate independent")
	}
	other := a.Key
	other.Trigger = "size_cap"
	if other.Hash() == a.Key.Hash() {
		t.Fatal("trigger type must be part of the key (resolves only its own trigger)")
	}
}

func TestSaveAndLoadAdjudications(t *testing.T) {
	data := t.TempDir()
	got, err := LoadAdjudications(data)
	if err != nil || len(got) != 0 {
		t.Fatalf("absent file: %v %v", got, err)
	}
	a := sampleAdj("accept")
	if err := SaveAdjudication(data, a); err != nil {
		t.Fatal(err)
	}
	// upsert: corrected decision replaces, never duplicates
	a2 := a
	a2.Decision = "reject"
	if err := SaveAdjudication(data, a2); err != nil {
		t.Fatal(err)
	}
	got, err = LoadAdjudications(data)
	if err != nil || len(got) != 1 {
		t.Fatalf("load: %v %v", got, err)
	}
	if got[a.KeyHash].Decision != "reject" {
		t.Fatalf("upsert lost: %+v", got[a.KeyHash])
	}

	bad := sampleAdj("maybe")
	if err := SaveAdjudication(data, bad); err == nil {
		t.Fatal("invalid decision must be rejected")
	}
	leaky := sampleAdj("accept")
	leaky.Note = "session 00000000-0000-4000-8000-000000000099"
	if err := SaveAdjudication(data, leaky); err == nil || !strings.Contains(err.Error(), "privacy") {
		t.Fatalf("privacy scan must block the write: %v", err)
	}
	// hand-edited key hash must fail loudly on load, not silently never match
	raw, err := os.ReadFile(filepath.Join(data, "adjudications.json"))
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), a.KeyHash, strings.Repeat("0", len(a.KeyHash)), 1)
	if err := os.WriteFile(filepath.Join(data, "adjudications.json"), []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAdjudications(data); err == nil {
		t.Fatal("stale key_hash must error on load")
	}
}
