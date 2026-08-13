package synthesis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fixtureBundle has one item of each kind plus a SessionDates entry ("sess-b")
// whose value coincides with neither From nor To, so it has no legitimate
// reason to appear anywhere else in the written bytes.
func fixtureBundle(repo string) EvidenceBundle {
	return EvidenceBundle{
		Repo:          repo,
		SessionCount:  3,
		AnalyzedCount: 3,
		From:          "2026-06-01",
		To:            "2026-06-10",
		Friction:      []FrictionItem{{ID: "F1", Type: "wrong_approach", OneLine: "x", SessionID: "sess-a"}},
		Prefs:         []PrefItem{{ID: "P1", Rule: "always y", Quote: "q", SessionID: "sess-b"}},
		Success:       []SuccessItem{{ID: "S1", Goal: "g", Summary: "s", SessionID: "sess-c"}},
		Signals: []OppSignal{{
			ID: "G1", Kind: "high_read", Magnitude: 3,
			MemberSessions: []string{"sess-a", "sess-b", "sess-c"},
		}},
		Context: ContextRollup{ToolMix: map[string]int{"Read": 12, "Edit": 4}},
		SessionDates: map[string]string{
			"sess-a": "2026-06-01", // == From, coincidental
			"sess-b": "2026-06-05", // distinct middle date: the leak canary
			"sess-c": "2026-06-10", // == To, coincidental
		},
	}
}

func writeFixture(t *testing.T, repo string) (path string, raw []byte, out EvidenceBundle) {
	t.Helper()
	dir := t.TempDir()
	bundles := map[string]EvidenceBundle{repo: fixtureBundle(repo)}
	paths, err := WriteBundleFiles(dir, bundles)
	if err != nil {
		t.Fatalf("WriteBundleFiles: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("paths = %d, want 1", len(paths))
	}
	wantPath := filepath.Join(dir, repo+"-bundle.json")
	if paths[0] != wantPath {
		t.Errorf("path = %q, want %q", paths[0], wantPath)
	}
	raw, err = os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal written file: %v", err)
	}
	return paths[0], raw, out
}

func TestWriteBundleFilesNamespacesIDs(t *testing.T) {
	_, _, out := writeFixture(t, "heyflow")

	if out.Friction[0].ID != "heyflow/F1" {
		t.Errorf("friction id = %q, want heyflow/F1", out.Friction[0].ID)
	}
	if out.Prefs[0].ID != "heyflow/P1" {
		t.Errorf("pref id = %q, want heyflow/P1", out.Prefs[0].ID)
	}
	if out.Success[0].ID != "heyflow/S1" {
		t.Errorf("success id = %q, want heyflow/S1", out.Success[0].ID)
	}
	if out.Signals[0].ID != "heyflow/G1" {
		t.Errorf("signal id = %q, want heyflow/G1", out.Signals[0].ID)
	}
	wantMembers := []string{"sess-a", "sess-b", "sess-c"}
	if !reflect.DeepEqual(out.Signals[0].MemberSessions, wantMembers) {
		t.Errorf("member_sessions = %v, want %v (untouched, not namespaced)", out.Signals[0].MemberSessions, wantMembers)
	}
}

func TestWriteBundleFilesStripsDates(t *testing.T) {
	_, raw, _ := writeFixture(t, "heyflow")
	s := string(raw)

	if strings.Contains(s, "session_dates") {
		t.Error("written bytes contain the session_dates key")
	}
	// sess-b's date isn't From or To, so its presence anywhere in the bytes
	// can only mean SessionDates leaked despite the key being gone.
	if strings.Contains(s, "2026-06-05") {
		t.Error("written bytes contain a per-session date value (SessionDates leaked)")
	}
}

func TestWriteBundleFilesToolMixSurvives(t *testing.T) {
	_, _, out := writeFixture(t, "heyflow")
	if out.Context.ToolMix["Read"] != 12 || out.Context.ToolMix["Edit"] != 4 {
		t.Errorf("tool_mix = %v, want Read:12 Edit:4", out.Context.ToolMix)
	}
}

func TestWriteBundleFilesDoesNotMutateInput(t *testing.T) {
	dir := t.TempDir()
	bundles := map[string]EvidenceBundle{"heyflow": fixtureBundle("heyflow")}
	if _, err := WriteBundleFiles(dir, bundles); err != nil {
		t.Fatalf("WriteBundleFiles: %v", err)
	}
	b := bundles["heyflow"]
	if b.Friction[0].ID != "F1" {
		t.Errorf("input bundle mutated: friction id = %q, want F1", b.Friction[0].ID)
	}
	if b.Signals[0].ID != "G1" {
		t.Errorf("input bundle mutated: signal id = %q, want G1", b.Signals[0].ID)
	}
	want := map[string]string{"sess-a": "2026-06-01", "sess-b": "2026-06-05", "sess-c": "2026-06-10"}
	if !reflect.DeepEqual(b.SessionDates, want) {
		t.Errorf("input bundle SessionDates mutated/lost: %v, want %v", b.SessionDates, want)
	}
}

func TestSplitNamespacedID(t *testing.T) {
	repo, id, ok := SplitNamespacedID("heyflow/F3")
	if !ok || repo != "heyflow" || id != "F3" {
		t.Errorf("SplitNamespacedID(heyflow/F3) = (%q,%q,%v), want (heyflow,F3,true)", repo, id, ok)
	}
	if _, _, ok := SplitNamespacedID("F3"); ok {
		t.Error("SplitNamespacedID(F3) ok = true, want false (no namespace separator)")
	}
}

func TestWriteBundleFilesMultipleRepos(t *testing.T) {
	dir := t.TempDir()
	bundles := map[string]EvidenceBundle{
		"heyflow": fixtureBundle("heyflow"),
		"alpha":   fixtureBundle("alpha"),
	}
	paths, err := WriteBundleFiles(dir, bundles)
	if err != nil {
		t.Fatalf("WriteBundleFiles: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %d, want 2", len(paths))
	}
	for _, repo := range []string{"heyflow", "alpha"} {
		if _, err := os.Stat(filepath.Join(dir, repo+"-bundle.json")); err != nil {
			t.Errorf("missing bundle file for %s: %v", repo, err)
		}
	}
}
