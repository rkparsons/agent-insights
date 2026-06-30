package insights

import (
	"testing"

	"tmux-ctrl/internal/userconfig"
)

func TestNewRepoResolver(t *testing.T) {
	cfg := &userconfig.Config{Repos: []userconfig.Repo{{Path: "/home/u/acme"}}}
	r := NewRepoResolver(cfg)
	if got := r("/home/u/acme/sub"); got != "/home/u/acme" {
		t.Errorf("match: got %q", got)
	}
	if got := r("/elsewhere"); got != "" {
		t.Errorf("unmatched should be empty: got %q", got)
	}
}
