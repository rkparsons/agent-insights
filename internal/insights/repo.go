package insights

import "tmux-ctrl/internal/userconfig"

// NewRepoResolver adapts userconfig.LookupRepo into the insights RepoResolver: a cwd
// maps to its repo path, or "" when unmatched (the catch-all bucket).
func NewRepoResolver(cfg *userconfig.Config) RepoResolver {
	return func(cwd string) string {
		if r := cfg.LookupRepo(cwd); r != nil {
			return r.Path
		}
		return ""
	}
}
