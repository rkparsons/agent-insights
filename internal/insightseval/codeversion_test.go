package insightseval

import "testing"

func TestCodeVersionStableAndPackageSensitive(t *testing.T) {
	v1, err := CodeVersion("internal/insightseval")
	if err != nil {
		t.Fatal(err)
	}
	v2, _ := CodeVersion("internal/insightseval")
	if v1 != v2 || len(v1) != 64 {
		t.Fatalf("unstable or malformed: %q vs %q", v1, v2)
	}
	other, err := CodeVersion("internal/insights")
	if err != nil {
		t.Fatal(err)
	}
	if other == v1 {
		t.Fatal("different packages must hash differently")
	}
	if _, err := CodeVersion("internal/does-not-exist"); err == nil {
		t.Fatal("missing package must error")
	}
}
