package synthesis

import (
	"reflect"
	"testing"
)

func TestLoadSynthesesCmd_MatchesLoadSyntheses(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", root)
	writeSynthesisJSON(t, root, "client-project", "2026-07-02", RepoSynthesis{Repo: "client-project", Meta: Meta{Model: "new"}})

	want, wantErr := LoadSyntheses()
	if wantErr != nil {
		t.Fatalf("LoadSyntheses: %v", wantErr)
	}

	msg, ok := LoadSynthesesCmd()().(SynthesesLoadedMsg)
	if !ok {
		t.Fatalf("LoadSynthesesCmd()() did not return a SynthesesLoadedMsg")
	}
	if msg.Err != nil {
		t.Fatalf("SynthesesLoadedMsg.Err = %v, want nil", msg.Err)
	}
	if !reflect.DeepEqual(msg.Syntheses, want) {
		t.Errorf("SynthesesLoadedMsg.Syntheses = %+v, want %+v", msg.Syntheses, want)
	}
}

func TestLoadSynthesesCmd_MissingDir_NoError(t *testing.T) {
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())

	msg, ok := LoadSynthesesCmd()().(SynthesesLoadedMsg)
	if !ok {
		t.Fatalf("LoadSynthesesCmd()() did not return a SynthesesLoadedMsg")
	}
	if msg.Err != nil || msg.Syntheses != nil {
		t.Fatalf("got (%v,%v), want (nil,nil) for missing synthesis dir", msg.Syntheses, msg.Err)
	}
}
