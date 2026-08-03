package eval

import "testing"

func TestCacheRoundTripAndMiss(t *testing.T) {
	c := NewCache(t.TempDir())
	type payload struct{ A string }
	var out payload
	hit, err := c.Get("l2", cacheKey("x"), &out)
	if err != nil || hit {
		t.Fatalf("miss: hit=%v err=%v", hit, err)
	}
	if err := c.Put("l2", cacheKey("x"), payload{A: "v"}); err != nil {
		t.Fatal(err)
	}
	hit, err = c.Get("l2", cacheKey("x"), &out)
	if err != nil || !hit || out.A != "v" {
		t.Fatalf("hit=%v err=%v out=%+v", hit, err, out)
	}
}

func TestCacheKeySensitivity(t *testing.T) {
	if cacheKey("a", "b") == cacheKey("ab") || cacheKey("a", "b") == cacheKey("a", "b", "") {
		t.Fatal("cacheKey must be injective over part boundaries")
	}
	if cacheKey("a", "b") != cacheKey("a", "b") {
		t.Fatal("cacheKey must be stable")
	}
}
