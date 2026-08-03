package insights

import (
	"os"
	"testing"
)

func TestLoadConfigMissingFileDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENT_INSIGHTS_CONFIG", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.CadenceDays != 14 {
		t.Errorf("CadenceDays = %d, want 14", cfg.CadenceDays)
	}
}

func TestLoadConfigEnvOverride(t *testing.T) {
	path := t.TempDir() + "/config.yaml"
	yaml := "repos:\n  - /a/b\naliases:\n  oldname: newname\ncadence_days: 14\nmin_sessions: 5\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_INSIGHTS_CONFIG", path)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0] != "/a/b" {
		t.Errorf("Repos = %v, want [/a/b]", cfg.Repos)
	}
	if cfg.Aliases["oldname"] != "newname" {
		t.Errorf("Aliases[oldname] = %q, want newname", cfg.Aliases["oldname"])
	}
	if cfg.CadenceDays != 14 {
		t.Errorf("CadenceDays = %d, want 14", cfg.CadenceDays)
	}
	if cfg.MinSessions != 5 {
		t.Errorf("MinSessions = %d, want 5", cfg.MinSessions)
	}
}

func TestResolverPrefixMatch(t *testing.T) {
	cfg := Config{Repos: []string{"/a/b"}}
	r := cfg.Resolver()
	if got := r("/a/b/x"); got != "/a/b" {
		t.Errorf("match: got %q, want /a/b", got)
	}
	if got := r("/a/bc"); got != "" {
		t.Errorf("boundary non-match: got %q, want \"\"", got)
	}
}

func TestCanonicalAlias(t *testing.T) {
	cfg := Config{Aliases: map[string]string{"oldname": "newname"}}
	if got := cfg.Canonical("oldname"); got != "newname" {
		t.Errorf("Canonical(oldname) = %q, want newname", got)
	}
	if got := cfg.Canonical("unknown"); got != "unknown" {
		t.Errorf("Canonical(unknown) = %q, want passthrough", got)
	}
}
