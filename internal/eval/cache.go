package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Cache is the local content-addressed stage cache: one JSON file per
// (stage, key). It lives outside the data repo — raw stage outputs are never
// committed fixtures.
type Cache struct{ root string }

func NewCache(root string) *Cache { return &Cache{root: root} }

func (c *Cache) path(stage, key string) string {
	return filepath.Join(c.root, stage, key+".json")
}

// Get unmarshals the cached value into v. ok=false with a nil error means a
// clean miss; a corrupt cache entry is an error, never silently recomputed.
func (c *Cache) Get(stage, key string, v any) (bool, error) {
	raw, err := os.ReadFile(c.path(stage, key))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return false, fmt.Errorf("corrupt cache entry %s/%s: %w", stage, key, err)
	}
	return true, nil
}

func (c *Cache) Put(stage, key string, v any) error {
	return writeJSON(c.path(stage, key), v)
}

// cacheKey hashes the parts with NUL separators so part boundaries are
// unambiguous ("a","b" never collides with "ab").
func cacheKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sha256hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
