package insights

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func lockPath() string { return filepath.Join(InsightsDir(), "insights.lock") }

// RunLock is an advisory whole-run lock held via flock on an open file descriptor.
// It is released automatically when the process exits or dies (no stale-PID class).
type RunLock struct{ f *os.File }

// AcquireLock takes a non-blocking exclusive flock. If another insights run holds it,
// it returns an error rather than blocking.
func AcquireLock() (*RunLock, error) {
	if err := os.MkdirAll(InsightsDir(), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another insights run is in progress (lock %s held): %w", lockPath(), err)
	}
	// Body is human diagnostics only; the flock — not this content — is the lock.
	f.Truncate(0)
	f.Seek(0, 0)
	fmt.Fprintf(f, "{\"pid\":%d,\"started\":%q}\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	return &RunLock{f: f}, nil
}

// Release drops the flock and closes the handle.
func (l *RunLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	cerr := l.f.Close()
	l.f = nil
	if err != nil {
		return err
	}
	return cerr
}
