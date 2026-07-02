package synthesis

import "testing"

func TestActedKey_StableAndRepoScoped(t *testing.T) {
	a := Recommendation{Statement: "Verify claims  before  asserting"}
	b := Recommendation{Statement: "verify claims before asserting"} // case/space-normalized same
	if ActedKey(a, "client-project") != ActedKey(b, "client-project") {
		t.Error("normalization: differently-spaced/cased identical statements must share a key")
	}
	if ActedKey(a, "client-project") == ActedKey(a, "tmux-ctrl") {
		t.Error("source repo must scope the key")
	}
}

func TestActedRoundTrip(t *testing.T) {
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	m, err := LoadActedKeys()
	if err != nil || len(m) != 0 {
		t.Fatalf("empty load = (%v,%v), want ({},nil)", m, err)
	}
	k := ActedKey(Recommendation{Statement: "do the thing"}, "client-project")
	if err := MarkActed(k); err != nil {
		t.Fatalf("MarkActed: %v", err)
	}
	m2, _ := LoadActedKeys()
	if !m2[k] {
		t.Errorf("key %q not persisted; got %v", k, m2)
	}
}
