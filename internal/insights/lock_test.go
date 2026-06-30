package insights

import "testing"

func TestLockSecondAcquireRefused(t *testing.T) {
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	l1, err := AcquireLock()
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if _, err := AcquireLock(); err == nil {
		t.Error("second acquire should be refused while the first is held")
	}
	if err := l1.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := AcquireLock()
	if err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
	l2.Release()
}
