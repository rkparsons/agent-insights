package synthesis

import (
	"sync"
	"testing"
)

func TestActedKey_StableAndRepoScoped(t *testing.T) {
	a := Recommendation{Statement: "Verify claims  before  asserting"}
	b := Recommendation{Statement: "verify claims before asserting"} // case/space-normalized same
	if ActedKey(a, "alpha") != ActedKey(b, "alpha") {
		t.Error("normalization: differently-spaced/cased identical statements must share a key")
	}
	if ActedKey(a, "alpha") == ActedKey(a, "tmux-ctrl") {
		t.Error("source repo must scope the key")
	}
}

func TestActedKey_TypeScoped(t *testing.T) {
	skill := Recommendation{Type: "new_skill", Statement: "address the foo friction"}
	hook := Recommendation{Type: "hook", Statement: "address the foo friction"}
	if ActedKey(skill, "alpha") == ActedKey(hook, "alpha") {
		t.Error("recommendation type must scope the key: same normalized statement, different type must not collide")
	}
}

func TestActedRoundTrip(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	m, err := LoadActedKeys()
	if err != nil || len(m) != 0 {
		t.Fatalf("empty load = (%v,%v), want ({},nil)", m, err)
	}
	k := ActedKey(Recommendation{Statement: "do the thing"}, "alpha")
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
	keep := ActedKey(Recommendation{Statement: "keep me"}, "alpha")
	drop := ActedKey(Recommendation{Statement: "roll me back"}, "alpha")
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
	if err := UnmarkActed(ActedKey(Recommendation{Statement: "never marked"}, "alpha")); err != nil {
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
	k1 := ActedKey(Recommendation{Statement: "concurrent one"}, "alpha")
	k2 := ActedKey(Recommendation{Statement: "concurrent two"}, "alpha")

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
