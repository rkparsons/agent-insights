// Package insightseval implements the outcome-eval suite for the insights
// pipeline: corpus freezing now; harness, rubrics, and scoring in later phases.
// See docs/superpowers/specs/2026-07-03-insights-outcome-eval-design.md.
package insightseval

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// freezeFile gzip-compresses src to dst and returns the sha256 (hex) of the raw
// bytes. Fixtures are append-only: an existing dst with identical content is
// skipped; differing content is an error.
func freezeFile(src, dst string) (string, int64, error) {
	raw, err := os.ReadFile(src)
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(raw)
	sha := hex.EncodeToString(sum[:])
	if existing, err := frozenSHA(dst); err == nil {
		if existing == sha {
			return sha, int64(len(raw)), nil
		}
		return "", 0, fmt.Errorf("append-only violation: %s exists with different content", dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", 0, fmt.Errorf("unable to check frozen file %s: %w", dst, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return "", 0, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	zw := gzip.NewWriter(tmp)
	if _, err := zw.Write(raw); err != nil {
		tmp.Close()
		return "", 0, err
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		return "", 0, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return "", 0, err
	}
	return sha, int64(len(raw)), nil
}

// frozenSHA returns the sha256 (hex) of a frozen file's decompressed content.
func frozenSHA(dst string) (string, error) {
	f, err := os.Open(dst)
	if err != nil {
		return "", err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	h := sha256.New()
	if _, err := io.Copy(h, zr); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
