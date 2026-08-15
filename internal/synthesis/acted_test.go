package synthesis

import (
	"strings"
	"sync"
	"testing"
)

func TestActedKey_NormalizesStatement(t *testing.T) {
	a := ActedKey("habit", false, "Verify claims  before  asserting")
	b := ActedKey("habit", false, "verify claims before asserting") // case/space-normalized same
	if a != b {
		t.Error("normalization: differently-spaced/cased identical statements must share a key")
	}
	if len(a) != 16 {
		t.Errorf("key %q, want a 16-char digest", a)
	}
}

func TestActedKey_TypeScoped(t *testing.T) {
	if ActedKey("new_skill", false, "address the foo friction") == ActedKey("hook", false, "address the foo friction") {
		t.Error("asset type must scope the key: same normalized statement, different type must not collide")
	}
}

// TestActedKey_LengthAndCharset pins the stored key's shape: acted keys are
// passed back in on the command line (`insights acted <key>`) and compared as
// opaque strings by every consumer.
func TestActedKey_LengthAndCharset(t *testing.T) {
	k := ActedKey("hook", false, "Run the smoke test before calling a task done.")
	if len(k) != 16 {
		t.Fatalf("key %q, want 16 chars", k)
	}
	for _, r := range k {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("key %q must be lowercase hex", k)
		}
	}
}

func TestActedRoundTrip(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	m, err := LoadActedKeys()
	if err != nil || len(m) != 0 {
		t.Fatalf("empty load = (%v,%v), want ({},nil)", m, err)
	}
	k := ActedKey("habit", false, "do the thing")
	if err := MarkActed(k); err != nil {
		t.Fatalf("MarkActed: %v", err)
	}
	m2, _ := LoadActedKeys()
	if !m2[k] {
		t.Errorf("key %q not persisted; got %v", k, m2)
	}
}

func TestUnmarkActed_RemovesKeyPreservingOthers(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	keep := ActedKey("habit", false, "keep me")
	drop := ActedKey("habit", false, "roll me back")
	if err := MarkActed(keep); err != nil {
		t.Fatalf("MarkActed keep: %v", err)
	}
	if err := MarkActed(drop); err != nil {
		t.Fatalf("MarkActed drop: %v", err)
	}

	if err := UnmarkActed(drop); err != nil {
		t.Fatalf("UnmarkActed: %v", err)
	}

	m, _ := LoadActedKeys()
	if m[drop] {
		t.Errorf("key %q still present after UnmarkActed", drop)
	}
	if !m[keep] {
		t.Errorf("UnmarkActed dropped the wrong key: %q no longer present", keep)
	}
}

func TestUnmarkActed_AbsentKeyIsNoop(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	if err := UnmarkActed(ActedKey("habit", false, "never marked")); err != nil {
		t.Errorf("UnmarkActed on absent key = %v, want nil (no-op)", err)
	}
}

// TestMarkActed_ConcurrentWritesBothLand guards the read-modify-write race:
// without the acted-file lock, two concurrent MarkActed calls can both read
// the same pre-write state and the second writer's atomicWrite silently
// drops the first writer's key. The flock in MarkActed must serialize them
// so both land.
func TestMarkActed_ConcurrentWritesBothLand(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	k1 := ActedKey("habit", false, "concurrent one")
	k2 := ActedKey("habit", false, "concurrent two")

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs <- MarkActed(k1) }()
	go func() { defer wg.Done(); errs <- MarkActed(k2) }()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("MarkActed: %v", err)
		}
	}

	m, err := LoadActedKeys()
	if err != nil {
		t.Fatal(err)
	}
	if !m[k1] || !m[k2] {
		t.Fatalf("both concurrent keys should land: %v", m)
	}
}

// TestActedKey_EscalationScoped: the escalated bit must scope the key — acting
// on a plain rule must not swallow a later escalation of the same statement
// (the escalation exists because the rule is not working).
func TestActedKey_EscalationScoped(t *testing.T) {
	s := "Open a draft PR before requesting review."
	if ActedKey("repo_doc", false, s) == ActedKey("repo_doc", true, s) {
		t.Error("same type and statement with and without escalation must not collide")
	}
}

// TestActedKey_NonEscalatedFormulaUnchanged pins the non-escalated hash input
// to the pre-v3 two-arg formula's output: keys already stored in
// insights-acted.json must stay valid across the v3 contract change.
func TestActedKey_NonEscalatedFormulaUnchanged(t *testing.T) {
	if k := ActedKey("hook", false, "Run the smoke test before calling a task done."); k != "21951a94294cb17f" {
		t.Errorf("key = %q, want pre-v3 digest 21951a94294cb17f", k)
	}
	if k := ActedKey("habit", false, "do the thing"); k != "2f98ce8bffc00b2f" {
		t.Errorf("key = %q, want pre-v3 digest 2f98ce8bffc00b2f", k)
	}
}
