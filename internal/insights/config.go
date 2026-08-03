package insights

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// defaultCadenceDays is the pipeline's synthesis due-cadence fallback when
// unset — 14, matching the TUI's historical default (internal/userconfig)
// so extracting the pipeline's own config doesn't silently double synthesis
// spend by halving the cadence.
const defaultCadenceDays = 14

// defaultConfigMinSessions mirrors synthesis.DefaultMinSessions (10); insights
// cannot import synthesis (which imports insights), so the value is duplicated.
const defaultConfigMinSessions = 10

// Config is agent-insights' own pipeline config: the repos it knows about, their
// aliases, and synthesis cadence/floor. The pipeline owns this file so it no
// longer depends on the TUI's config package.
type Config struct {
	Repos       []string          `yaml:"repos"`        // absolute paths
	Aliases     map[string]string `yaml:"aliases"`      // old-name -> canonical
	CadenceDays int               `yaml:"cadence_days"` // default 14
	MinSessions int               `yaml:"min_sessions"` // default synthesis.DefaultMinSessions
}

// LoadConfig reads ~/.config/agent-insights/config.yaml. AGENT_INSIGHTS_CONFIG,
// when set, overrides the full path (tests use this to avoid touching $HOME). A
// missing file returns zero-value defaults, not an error.
func LoadConfig() (Config, error) {
	path := os.Getenv("AGENT_INSIGHTS_CONFIG")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Config{}, err
		}
		path = filepath.Join(home, ".config", "agent-insights", "config.yaml")
	}
	return loadConfigFromPath(path)
}

func loadConfigFromPath(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{CadenceDays: defaultCadenceDays, MinSessions: defaultConfigMinSessions}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Config{}, err
	}
	if c.CadenceDays <= 0 {
		c.CadenceDays = defaultCadenceDays
	}
	if c.MinSessions <= 0 {
		c.MinSessions = defaultConfigMinSessions
	}
	return c, nil
}

// Resolver returns a RepoResolver that path-prefix matches a cwd against Repos:
// component-boundary matching, so "/a/b" does not match "/a/bc/...". "" when no
// configured repo matches.
func (c Config) Resolver() RepoResolver {
	return func(cwd string) string {
		for _, r := range c.Repos {
			if cwd == r || strings.HasPrefix(cwd, r+string(filepath.Separator)) {
				return r
			}
		}
		return ""
	}
}

// Canonical looks up name in Aliases, returning its canonical repo key; an
// unaliased name passes through unchanged.
func (c Config) Canonical(name string) string {
	if alias, ok := c.Aliases[name]; ok {
		return alias
	}
	return name
}

var warnNoReposOnce sync.Once

// WarnIfNoRepos prints one stderr line, once per process, when c has no
// configured repos (missing config file or an explicitly empty repos list).
// Grouping entry points (synthesize, eval freeze/benchmark) call this after
// LoadConfig so an empty config degrades visibly instead of silently falling
// back to path heuristics for every session.
func (c Config) WarnIfNoRepos() {
	if len(c.Repos) > 0 {
		return
	}
	warnNoReposOnce.Do(func() {
		fmt.Fprintln(os.Stderr, "agent-insights: no repos configured (~/.config/agent-insights/config.yaml); grouping falls back to path heuristics")
	})
}
