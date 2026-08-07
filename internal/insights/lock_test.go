package insights

import "testing"

func TestLockSecondAcquireRefused(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	l1, err := AcquireLock("analyze")
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if _, err := AcquireLock("analyze"); err == nil {
		t.Error("second acquire should be refused while the first is held")
	}
	if err := l1.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := AcquireLock("analyze")
	if err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
	l2.Release()
}

func TestLockHeld(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	if LockHeld() {
		t.Fatal("no lock file yet: want false")
	}
	l, err := AcquireLock("analyze")
	if err != nil {
		t.Fatal(err)
	}
	if !LockHeld() {
		t.Fatal("lock acquired: want true")
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if LockHeld() {
		t.Fatal("lock released: want false")
	}
}

func TestHeldOpReportsHolderOp(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	lock, err := AcquireLock("synthesize")
	if err != nil {
		t.Fatal(err)
	}
	if got := HeldOp(); got != "synthesize" {
		t.Errorf("HeldOp while held = %q, want %q", got, "synthesize")
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if got := HeldOp(); got != "" {
		t.Errorf("HeldOp after release = %q, want empty", got)
	}
}
