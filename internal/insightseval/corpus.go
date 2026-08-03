package insightseval

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"tmux-ctrl/internal/transcript"
)

// Corpus reads the frozen transcript corpus, synthesizing transcript.TranscriptRefs
// for the pipeline's consumers. Frozen files are canonical: every materialized
// plain transcript is verified against its manifest sha before use.
type Corpus struct {
	dataDir      string
	plainDir     string
	manifest     Manifest
	manifestHash string
	entries      map[string]ManifestEntry
	sidechains   map[string][]SidechainEntry
}

func OpenCorpus(dataDir, plainDir string) (*Corpus, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("open corpus: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	c := &Corpus{
		dataDir: dataDir, plainDir: plainDir, manifest: m,
		manifestHash: sha256hex(raw),
		entries:      make(map[string]ManifestEntry, len(m.Entries)),
		sidechains:   map[string][]SidechainEntry{},
	}
	for _, e := range m.Entries {
		c.entries[e.SessionID] = e
	}
	for _, s := range m.Sidechains {
		c.sidechains[s.ParentSessionID] = append(c.sidechains[s.ParentSessionID], s)
	}
	return c, nil
}

func (c *Corpus) ManifestHash() string { return c.manifestHash }

func (c *Corpus) Has(id string) bool { _, ok := c.entries[id]; return ok }

func (c *Corpus) Entry(id string) (ManifestEntry, bool) {
	e, ok := c.entries[id]
	return e, ok
}

// Ref materializes the frozen transcript into plainDir and returns a
// TranscriptRef whose Mtime is the manifest's (freeze-time) mtime, so
// downstream stamping matches the frozen world, not the extraction time.
func (c *Corpus) Ref(id string) (transcript.TranscriptRef, error) {
	e, ok := c.entries[id]
	if !ok {
		return transcript.TranscriptRef{}, fmt.Errorf("corpus: no frozen transcript for %s (recorded gap?)", id)
	}
	plain := filepath.Join(c.plainDir, id+".jsonl")
	gz := filepath.Join(c.dataDir, "corpus", id+".jsonl.gz")
	if err := materialize(gz, plain, e.SHA256, e.Mtime); err != nil {
		return transcript.TranscriptRef{}, err
	}
	return transcript.TranscriptRef{SessionID: id, Path: plain, Mtime: e.Mtime}, nil
}

// SidechainFiles returns the raw sidechain entries (jsonl + meta.json) mapped
// to a parent session — the mapping M2's future detection needs.
func (c *Corpus) SidechainFiles(parentID string) []SidechainEntry {
	return c.sidechains[parentID]
}

// SidechainRefs materializes the parent's .jsonl sidechains (meta files are
// metadata, not transcripts) under plainDir/sidechains/<parent>/.
func (c *Corpus) SidechainRefs(parentID string) ([]transcript.TranscriptRef, error) {
	var out []transcript.TranscriptRef
	for _, s := range c.sidechains[parentID] {
		if filepath.Ext(s.File) != ".jsonl" {
			continue
		}
		plain := filepath.Join(c.plainDir, "sidechains", parentID, s.File)
		gz := filepath.Join(c.dataDir, "corpus-sidechains", parentID, s.File+".gz")
		if err := materialize(gz, plain, s.SHA256, s.Mtime); err != nil {
			return nil, err
		}
		out = append(out, transcript.TranscriptRef{SessionID: parentID, Path: plain, Mtime: s.Mtime})
	}
	return out, nil
}

// materialize gunzips gz to plain once and verifies content against wantSHA
// both on extraction and on every reuse — a mismatched plain file is an error,
// never silently rebuilt (it may indicate cache tampering or a manifest edit).
func materialize(gz, plain, wantSHA string, mtime time.Time) error {
	if raw, err := os.ReadFile(plain); err == nil {
		if sha256hex(raw) == wantSHA {
			return nil
		}
		return fmt.Errorf("materialized transcript %s does not match manifest sha", plain)
	}
	f, err := os.Open(gz)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s: %w", gz, err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		return fmt.Errorf("%s: %w", gz, err)
	}
	if got := sha256hex(raw); got != wantSHA {
		return fmt.Errorf("frozen transcript %s does not match manifest sha (corpus tamper?)", gz)
	}
	if err := os.MkdirAll(filepath.Dir(plain), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(plain), "."+filepath.Base(plain)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, plain); err != nil {
		return err
	}
	return os.Chtimes(plain, mtime, mtime)
}
