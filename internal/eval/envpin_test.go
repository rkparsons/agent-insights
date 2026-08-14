package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustWriteFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// withFakeCredentials swaps credentialsCommand for a real-but-harmless command
// so tests never touch the actual macOS keychain, restoring the original on
// cleanup.
func withFakeCredentials(t *testing.T) {
	t.Helper()
	orig := credentialsCommand
	credentialsCommand = []string{"echo", `{"fake":"credential"}`}
	t.Cleanup(func() { credentialsCommand = orig })
}

func TestHashTreeStableAndContentSensitive(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.md"), "one")
	mustWriteFile(t, filepath.Join(dir, "sub", "b.md"), "two")
	h1, err := hashTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := hashTree(dir)
	if h1 != h2 {
		t.Fatal("unstable")
	}
	mustWriteFile(t, filepath.Join(dir, "a.md"), "changed")
	h3, _ := hashTree(dir)
	if h3 == h1 {
		t.Fatal("content change must change the hash")
	}
	// symlinked root resolves (stow-managed skill dirs)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	h4, err := hashTree(link)
	if err != nil {
		t.Fatal(err)
	}
	if h4 != h3 {
		t.Fatal("symlinked root must hash like its target")
	}
	// a symlinked directory NESTED in the tree is followed too (stow can
	// symlink subtrees; copyTree follows them, so the hash must match scope)
	realSub := t.TempDir()
	mustWriteFile(t, filepath.Join(realSub, "c.md"), "three")
	if err := os.Symlink(realSub, filepath.Join(dir, "nested")); err != nil {
		t.Fatal(err)
	}
	h5, err := hashTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h5 == h3 {
		t.Fatal("nested symlinked dir content must be hashed")
	}
}

func TestComposeEnvPinBuildsEphemeralConfig(t *testing.T) {
	withFakeCredentials(t)
	data, scratch := t.TempDir(), t.TempDir()
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "global", "CLAUDE.md"), "frozen rules")
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "global", "settings.json"), "{}")
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "global", "skills", "synthesizing-workflow-insights", "SKILL.md"), "frozen skill")
	liveSkill := t.TempDir()
	mustWriteFile(t, filepath.Join(liveSkill, "SKILL.md"), "live skill v2")

	pin, err := ComposeEnvPin(data, scratch, map[string]string{"synthesizing-workflow-insights": liveSkill}, "1.2.3 (Claude Code)", "claude-fable-5")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(pin.ConfigDir, "skills", "synthesizing-workflow-insights", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "live skill v2" {
		t.Fatalf("live skill not overlaid: %q", got)
	}
	frozen, err := os.ReadFile(filepath.Join(pin.ConfigDir, "CLAUDE.md"))
	if err != nil || string(frozen) != "frozen rules" {
		t.Fatalf("frozen config not copied: %q %v", frozen, err)
	}
	credPath := filepath.Join(pin.ConfigDir, ".credentials.json")
	credRaw, err := os.ReadFile(credPath)
	if err != nil || string(credRaw) != `{"fake":"credential"}` {
		t.Fatalf("credentials not materialized: %q %v", credRaw, err)
	}
	info, err := os.Stat(credPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials.json mode = %v, want 0600", info.Mode().Perm())
	}
	entries, err := os.ReadDir(pin.WorkDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("scratch cwd must exist empty: %v %v", entries, err)
	}
	if pin.EnvHash == "" || pin.SnapshotHash == "" || pin.SkillHashes["synthesizing-workflow-insights"] == "" {
		t.Fatalf("hashes: %+v", pin)
	}
	// env hash covers the pristine snapshot, not the overlaid copy
	pin2, err := ComposeEnvPin(data, t.TempDir(), map[string]string{"synthesizing-workflow-insights": liveSkill}, "1.2.3 (Claude Code)", "claude-fable-5")
	if err != nil {
		t.Fatal(err)
	}
	if pin2.EnvHash != pin.EnvHash {
		t.Fatal("env hash must be stable for the same snapshot + claude version")
	}
	pin3, _ := ComposeEnvPin(data, t.TempDir(), map[string]string{"synthesizing-workflow-insights": liveSkill}, "9.9.9", "claude-fable-5")
	if pin3.EnvHash == pin.EnvHash {
		t.Fatal("claude version bump must change the env hash")
	}
}

// The configured L2 model is pinned, but only for the L2 stage: EnvHash also
// keys L1 judgments and the matcher/probe caches, none of which can see the
// synthesis model, so folding it in there would orphan paid entries a model
// switch cannot have changed.
func TestComposeEnvPinModelKeysL2Only(t *testing.T) {
	withFakeCredentials(t)
	data := t.TempDir()
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "global", "CLAUDE.md"), "frozen")

	fable, err := ComposeEnvPin(data, t.TempDir(), nil, "1.0.0", "claude-fable-5")
	if err != nil {
		t.Fatal(err)
	}
	opus, err := ComposeEnvPin(data, t.TempDir(), nil, "1.0.0", "claude-opus-5")
	if err != nil {
		t.Fatal(err)
	}
	if fable.SynthesisModel != "claude-fable-5" {
		t.Fatalf("pin must carry the configured model: %q", fable.SynthesisModel)
	}
	if fable.EnvHash != opus.EnvHash {
		t.Fatal("the synthesis model must NOT enter EnvHash (it would orphan the L1 and matcher caches)")
	}
	if fable.L2EnvHash == opus.L2EnvHash {
		t.Fatal("a model switch must change the L2 env hash")
	}
	bumped, err := ComposeEnvPin(data, t.TempDir(), nil, "9.9.9", "claude-fable-5")
	if err != nil {
		t.Fatal(err)
	}
	if bumped.L2EnvHash == fable.L2EnvHash {
		t.Fatal("the L2 env hash must still track the environment it is built on")
	}
}

func TestSnapshotSkillsAppendOnlyFirstSight(t *testing.T) {
	data := t.TempDir()
	skill := t.TempDir()
	mustWriteFile(t, filepath.Join(skill, "SKILL.md"), "v1")
	h, err := hashTree(skill)
	if err != nil {
		t.Fatal(err)
	}
	warnings, err := snapshotSkills(data, map[string]string{"s": skill}, map[string]string{"s": h})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "first snapshot") {
		t.Fatalf("first sight: %v", warnings)
	}
	snap := filepath.Join(data, "skill-snapshots", h, "s", "SKILL.md")
	if raw, err := os.ReadFile(snap); err != nil || string(raw) != "v1" {
		t.Fatalf("snapshot missing: %v %q", err, raw)
	}
	warnings, err = snapshotSkills(data, map[string]string{"s": skill}, map[string]string{"s": h})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("second sight must be a no-op: %v", warnings)
	}
	// a changed skill (new hash, prior snapshot exists) warns as drift
	mustWriteFile(t, filepath.Join(skill, "SKILL.md"), "v2")
	h2, err := hashTree(skill)
	if err != nil {
		t.Fatal(err)
	}
	warnings, err = snapshotSkills(data, map[string]string{"s": skill}, map[string]string{"s": h2})
	if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "changed since") {
		t.Fatalf("drift: %v %v", warnings, err)
	}
}

func TestComposeEnvPinWarnsOnHookDrift(t *testing.T) {
	withFakeCredentials(t)
	data, scratch := t.TempDir(), t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "global", "settings.json"), "{}")
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "global", "hooks", "h.sh"), "frozen hook")
	mustWriteFile(t, filepath.Join(home, ".claude", "hooks", "h.sh"), "live hook CHANGED")
	pin, err := ComposeEnvPin(data, scratch, nil, "1.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(pin.DriftWarnings, "\n")
	if !strings.Contains(joined, "hooks") {
		t.Fatalf("expected hook drift warning, got %v", pin.DriftWarnings)
	}
}

func TestComposeEnvPinFailsWhenCredentialUnavailable(t *testing.T) {
	orig := credentialsCommand
	credentialsCommand = []string{"false"}
	t.Cleanup(func() { credentialsCommand = orig })

	data, scratch := t.TempDir(), t.TempDir()
	mustWriteFile(t, filepath.Join(data, "config-snapshot", "global", "settings.json"), "{}")
	_, err := ComposeEnvPin(data, scratch, nil, "1.0.0", "")
	if err == nil || !strings.Contains(err.Error(), "keychain credential") {
		t.Fatalf("expected keychain credential error, got %v", err)
	}
}
