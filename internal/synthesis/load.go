package synthesis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoadSyntheses reads the newest-dated RepoSynthesis for every repo directory
// under synthesis/. Malformed or unreadable files are skipped (a bad file must
// not blank the section); a missing synthesis dir returns (nil, nil). Result is
// sorted by Repo ascending for deterministic downstream ordering.
func LoadSyntheses() ([]RepoSynthesis, error) {
	base := synthesisDir()
	repoDirs, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []RepoSynthesis
	for _, rd := range repoDirs {
		if !rd.IsDir() {
			continue
		}
		s, ok := newestInRepoDir(filepath.Join(base, rd.Name()))
		if ok {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out, nil
}

// newestInRepoDir returns the newest parseable <date>.json in a repo dir.
// Filenames are YYYY-MM-DD so lexical desc == chronological desc.
func newestInRepoDir(dir string) (RepoSynthesis, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return RepoSynthesis{}, false
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names { // newest first; skip malformed
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var s RepoSynthesis
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		return s, true
	}
	return RepoSynthesis{}, false
}
