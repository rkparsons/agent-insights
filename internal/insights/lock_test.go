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

func TestAcquireLockRejectsUnknownOp(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	if _, err := AcquireLock("bogus"); err == nil {
		t.Fatal("expected an error for an op not in LockOps")
	}
	if LockHeld() {
		t.Fatal("a rejected op must not leave the lock held")
	}
}

// LockOps and status.schema.json's running_op enum are two independent
// listings of the same set of values; a future op added to one but not the
// other must fail loudly rather than let status output silently violate its
// own schema.
func TestLockOpsMatchesStatusSchemaRunningOpEnum(t *testing.T) {
	schema := loadSchema(t, "status.schema.json")
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("status.schema.json has no top-level properties")
	}
	runningOp, ok := props["running_op"].(map[string]any)
	if !ok {
		t.Fatal("status.schema.json properties.running_op missing or not an object")
	}
	enumRaw, ok := runningOp["enum"].([]any)
	if !ok {
		t.Fatal("status.schema.json properties.running_op.enum missing or not an array")
	}
	schemaOps := map[string]bool{}
	for _, v := range enumRaw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("running_op enum entry %v is not a string", v)
		}
		schemaOps[s] = true
	}
	lockOps := map[string]bool{}
	for _, op := range LockOps {
		lockOps[op] = true
	}
	if diff := setDiff(schemaOps, lockOps); diff != "" {
		t.Errorf("status.schema.json running_op.enum vs LockOps: %s", diff)
	}
}
