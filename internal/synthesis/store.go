package synthesis

import (
	"encoding/json"
	"os"
	"path/filepath"

	"tmux-ctrl/internal/insights"
)

func synthesisDir() string { return filepath.Join(insights.InsightsDir(), "synthesis") }

func Store(s RepoSynthesis, md string, date string) error {
	dir := filepath.Join(synthesisDir(), s.Repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, date+".json"), data); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, date+".md"), []byte(md))
}

func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
