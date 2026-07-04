package insightseval

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
)

// srcRoot resolves the module root (src/) from this file's compile-time path.
// Works whenever the binary was built from a checkout that still exists —
// the normal dev loop. Release binaries without the tree fall back to VCS
// build info in CodeVersion.
var srcRoot = sync.OnceValues(func() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("no caller info")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return "", fmt.Errorf("module root not found at %s: %w", root, err)
	}
	return root, nil
})

// CodeVersion content-hashes the non-test Go sources of the named src-relative
// packages (e.g. "internal/insights"). Deterministic stages must never serve
// pre-change output after a Go edit, so this — not a build timestamp — keys
// their cache entries. Falls back to a clean VCS revision when the source
// tree is unavailable; a dirty tree without sources is an error.
func CodeVersion(pkgs ...string) (string, error) {
	root, err := srcRoot()
	if err != nil {
		if bi, ok := debug.ReadBuildInfo(); ok {
			var rev, modified string
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					rev = s.Value
				case "vcs.modified":
					modified = s.Value
				}
			}
			if rev != "" && modified == "false" {
				return "vcs:" + rev, nil
			}
		}
		return "", fmt.Errorf("code version: no source tree and no clean vcs build info: %w", err)
	}
	h := sha256.New()
	for _, pkg := range pkgs {
		dir := filepath.Join(root, filepath.FromSlash(pkg))
		entries, err := os.ReadDir(dir) // sorted by name
		if err != nil {
			return "", fmt.Errorf("code version %s: %w", pkg, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return "", err
			}
			fmt.Fprintf(h, "%s/%s\x00%d\x00", pkg, name, len(data))
			h.Write(data)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
