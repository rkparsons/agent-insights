package synthesis

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// bundleFile is EvidenceBundle's on-disk shape for the global-synthesis scratch
// dir: same fields except SessionDates is structurally absent (not merely
// omitempty) so a dropped tag elsewhere can never let per-session dates reach
// the file the model reads — see the invariant at bundle.go:68-72.
type bundleFile struct {
	Repo          string         `json:"repo"`
	SessionCount  int            `json:"session_count"`
	AnalyzedCount int            `json:"analyzed_count"`
	From          string         `json:"from"`
	To            string         `json:"to"`
	Friction      []FrictionItem `json:"friction"`
	Prefs         []PrefItem     `json:"prefs"`
	Success       []SuccessItem  `json:"success"`
	Signals       []OppSignal    `json:"signals"`
	Context       ContextRollup  `json:"context"`
}

// WriteBundleFiles writes each bundle to dir/<repo>-bundle.json with every item id
// rewritten to "<repo>/<id>" and SessionDates stripped. Returns written paths.
// The repo used for namespacing and the filename is the map key, not
// EvidenceBundle.Repo, since that's what later verification code will look up
// by. Bundles are copied item-by-item into bundleFile, never mutated in place:
// the eval bundle cache and Go verification need the original bare ids and
// SessionDates intact.
func WriteBundleFiles(dir string, bundles map[string]EvidenceBundle) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	repos := make([]string, 0, len(bundles))
	for repo := range bundles {
		repos = append(repos, repo)
	}
	sort.Strings(repos) // deterministic write/return order

	paths := make([]string, 0, len(repos))
	for _, repo := range repos {
		data, err := marshalBundleFile(repo, bundles[repo])
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, repo+"-bundle.json")
		if err := atomicWrite(path, data); err != nil {
			return nil, fmt.Errorf("write bundle %q: %w", repo, err)
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// marshalBundleFile renders one bundle exactly as the model will read it —
// namespaced ids, no session dates — so a size taken from it (the --dry-run
// report) is the size that actually reaches the model's context.
func marshalBundleFile(repo string, b EvidenceBundle) ([]byte, error) {
	fb := bundleFile{
		Repo: b.Repo, SessionCount: b.SessionCount, AnalyzedCount: b.AnalyzedCount,
		From: b.From, To: b.To, Context: b.Context,
	}
	for _, f := range b.Friction {
		f.ID = namespaceID(repo, f.ID)
		fb.Friction = append(fb.Friction, f)
	}
	for _, p := range b.Prefs {
		p.ID = namespaceID(repo, p.ID)
		fb.Prefs = append(fb.Prefs, p)
	}
	for _, s := range b.Success {
		s.ID = namespaceID(repo, s.ID)
		fb.Success = append(fb.Success, s)
	}
	for _, sig := range b.Signals {
		sig.ID = namespaceID(repo, sig.ID) // MemberSessions are session ids, left untouched
		fb.Signals = append(fb.Signals, sig)
	}
	data, err := json.MarshalIndent(fb, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal bundle %q: %w", repo, err)
	}
	return data, nil
}

func namespaceID(repo, id string) string { return repo + "/" + id }

// SplitNamespacedID splits a "<repo>/<id>" id produced by WriteBundleFiles back
// into its parts. ok is false when namespaced has no "/" separator.
func SplitNamespacedID(namespaced string) (repo, id string, ok bool) {
	i := strings.IndexByte(namespaced, '/')
	if i < 0 {
		return "", "", false
	}
	return namespaced[:i], namespaced[i+1:], true
}
