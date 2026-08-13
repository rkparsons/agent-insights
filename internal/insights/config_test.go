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

// TestLoadConfigV2KeysDefaultOnV1OnlyFile pins the v2 compatibility contract: a
// config file written before v2 (only the v1 flat keys) must still parse, and
// the new v2 keys must come back at their documented defaults rather than zero
// values that would silently disable synthesis (empty model) or make every run
// due (due_new_sessions == 0).
func TestLoadConfigV2KeysDefaultOnV1OnlyFile(t *testing.T) {
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
	if cfg.SynthesisModel != "claude-fable-5" {
		t.Errorf("SynthesisModel = %q, want claude-fable-5", cfg.SynthesisModel)
	}
	if cfg.DueNewSessions != 10 {
		t.Errorf("DueNewSessions = %d, want 10", cfg.DueNewSessions)
	}
	if cfg.DotfilesRepo != "" {
		t.Errorf("DotfilesRepo = %q, want empty", cfg.DotfilesRepo)
	}
}

// TestLoadConfigV2KeysExplicit confirms explicit values for the new keys
// (including an explicit dotfiles_repo) round-trip rather than being clobbered
// by defaulting.
func TestLoadConfigV2KeysExplicit(t *testing.T) {
	path := t.TempDir() + "/config.yaml"
	yaml := "synthesis_model: claude-opus-5\ndue_new_sessions: 3\ndotfiles_repo: /Users/dev/Developer/dotfiles\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_INSIGHTS_CONFIG", path)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.SynthesisModel != "claude-opus-5" {
		t.Errorf("SynthesisModel = %q, want claude-opus-5", cfg.SynthesisModel)
	}
	if cfg.DueNewSessions != 3 {
		t.Errorf("DueNewSessions = %d, want 3", cfg.DueNewSessions)
	}
	if cfg.DotfilesRepo != "/Users/dev/Developer/dotfiles" {
		t.Errorf("DotfilesRepo = %q, want /Users/dev/Developer/dotfiles", cfg.DotfilesRepo)
	}
}

// TestLoadConfigMissingFileV2Defaults extends TestLoadConfigMissingFileDefaults:
// a wholly absent config file must still default the v2 keys, not just the v1
// ones, since LoadConfig's no-file branch builds its own Config{} literal.
func TestLoadConfigMissingFileV2Defaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENT_INSIGHTS_CONFIG", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.SynthesisModel != "claude-fable-5" {
		t.Errorf("SynthesisModel = %q, want claude-fable-5", cfg.SynthesisModel)
	}
	if cfg.DueNewSessions != 10 {
		t.Errorf("DueNewSessions = %d, want 10", cfg.DueNewSessions)
	}
	if cfg.DotfilesRepo != "" {
		t.Errorf("DotfilesRepo = %q, want empty", cfg.DotfilesRepo)
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
