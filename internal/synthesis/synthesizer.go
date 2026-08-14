package synthesis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/skills"
)

// synthesisSchema is the JSON schema passed to `claude -p --json-schema`,
// single-sourced from the embedded synthesizing-workflow-insights skill so the
// schema and the prompt that documents it cannot drift apart.
var synthesisSchema = string(skills.SynthesisSchema())

const (
	synthesisModel        = "claude-opus-4-8"
	synthesisSkillCommand = "/synthesizing-workflow-insights"
)

// SynthesisModel is the pinned L2 model id, exported for eval cache keys and
// reproducibility records.
const SynthesisModel = synthesisModel

// SchemaHash returns the sha256 (hex) of the embedded L2 schema, for eval
// cache keys and reproducibility records.
func SchemaHash() string {
	sum := sha256.Sum256([]byte(synthesisSchema))
	return hex.EncodeToString(sum[:])
}

// Synthesizer produces the qualitative themes/recommendations half of a repo's
// insights from its EvidenceBundle. Injected so the merge/ranking logic is
// testable with a fake and no real LLM.
type Synthesizer interface {
	Synthesize(ctx context.Context, b EvidenceBundle) (RawSynthesis, error)
}

// commandRunner runs the prepared claude command, feeding stdin and returning
// stdout. Injected so the envelope parsing + error handling are unit-testable.
type commandRunner func(ctx context.Context, stdin []byte) (stdout []byte, err error)

type claudeSynthesizer struct {
	run    commandRunner
	model  string
	schema string
}

func (s claudeSynthesizer) Synthesize(ctx context.Context, b EvidenceBundle) (RawSynthesis, error) {
	stdinBundle := b
	stdinBundle.SessionDates = nil // model must never see dates (it cannot emit numbers)
	stdin, err := json.Marshal(stdinBundle)
	if err != nil {
		return RawSynthesis{}, err
	}
	out, err := s.run(ctx, stdin)
	if err != nil {
		return RawSynthesis{}, fmt.Errorf("synthesis command: %w", err)
	}
	payload, err := insights.ParseClaudeEnvelope(out)
	if err != nil {
		return RawSynthesis{}, err
	}
	var raw RawSynthesis
	if err := json.Unmarshal(payload, &raw); err != nil {
		return RawSynthesis{}, fmt.Errorf("structured_output parse: %w", err)
	}
	return raw, nil
}

// scrubbedEnv returns the current environment with the API-key vars removed so the
// nested claude runs under subscription auth, never API billing. Removed, not
// blanked — an empty ANTHROPIC_API_KEY still wins its precedence slot.
func scrubbedEnv() []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, kv := range src {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// newSynthesizeCommand builds the claude invocation that runs the synthesis skill with
// structured output. The bundle is fed on stdin (argv is never used for it — bundles
// can exceed the macOS argv cap). workDir is required, not optional: the nested claude
// resolves /synthesizing-workflow-insights from its cwd, where the run materialized the
// skills package — an empty workDir would silently fall back to the caller's cwd and
// whatever skills happen to be ambient there.
func newSynthesizeCommand(ctx context.Context, model, schema string, stdin []byte, configDir, workDir string) (*exec.Cmd, error) {
	if workDir == "" {
		return nil, errors.New("synthesis workDir is empty: the run must materialize the skills into a scratch cwd (skills.TempWorkdir)")
	}
	cmd := exec.CommandContext(ctx, "claude", "-p", synthesisSkillCommand,
		"--output-format", "json",
		"--json-schema", schema,
		"--model", model,
		// Nested synthesis calls must NOT persist their own session transcripts —
		// otherwise a backfill re-run litters ~/.claude/projects with synthesizer
		// exhaust that then re-enters the scan as gated noise. The synthesis still
		// returns structured output on stdout; only the on-disk session record is
		// suppressed.
		"--no-session-persistence")
	cmd.Stdin = bytes.NewReader(stdin)
	// A context kill only signals the direct child; claude's own subprocesses
	// inherit the stdout pipe and can strand Output() long past the deadline
	// (observed: a 20m kill draining for hours). WaitDelay forcibly closes.
	cmd.WaitDelay = 30 * time.Second
	cmd.Env = scrubbedEnv()
	// Appended last so it wins over any inherited CLAUDE_CONFIG_DIR (os/exec
	// keeps the last duplicate). Pinning both knobs keeps a nested claude from
	// reading live global config or a project CLAUDE.md from the caller's cwd.
	if configDir != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+configDir)
	}
	cmd.Dir = workDir
	return cmd, nil
}

// SynthesizerFactory builds a run's Synthesizer once the run has materialized
// the skills package into workDir — the nested claude's cwd. The seam is a
// factory rather than a Synthesizer because only the run owns that directory's
// lifetime; tests pass a factory that ignores workDir and returns a fake.
type SynthesizerFactory func(workDir string) Synthesizer

// NewClaudeSynthesizer returns a Synthesizer that shells out to `claude -p` under
// subscription auth (Opus 4.8, embedded schema), running in workDir. The caller's
// ctx governs the subprocess timeout; a context with no deadline means no timeout.
func NewClaudeSynthesizer(workDir string) Synthesizer {
	return NewClaudeSynthesizerPinned("", workDir)
}

// NewClaudeSynthesizerPinned is NewClaudeSynthesizer with the nested claude's
// config dir pinned too (see NewClaudeJudgePinned). An empty configDir leaves
// that knob inherited; workDir is always required.
func NewClaudeSynthesizerPinned(configDir, workDir string) Synthesizer {
	s := claudeSynthesizer{model: synthesisModel, schema: synthesisSchema}
	s.run = func(ctx context.Context, stdin []byte) ([]byte, error) {
		cmd, err := newSynthesizeCommand(ctx, s.model, s.schema, stdin, configDir, workDir)
		if err != nil {
			return nil, err
		}
		out, err := cmd.Output()
		if err != nil {
			return out, wrapClaudeExit(out, err)
		}
		return out, nil
	}
	return s
}

// wrapClaudeExit formats a claude subprocess failure. claude -p reports many
// errors (e.g. "Not logged in") in its stdout JSON envelope with an empty
// stderr, so stdout's tail is included whenever stderr alone would be blank.
func wrapClaudeExit(out []byte, err error) error {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return err
	}
	detail := strings.TrimSpace(string(ee.Stderr))
	if detail == "" {
		detail = "stdout: " + strings.TrimSpace(string(out))
	}
	return fmt.Errorf("claude exit %d: %s", ee.ExitCode(), truncRunes(detail, 2000))
}

// truncRunes caps model- or CLI-authored text before it reaches an error
// string: those errors are recorded in the run state on disk, so their length
// is bounded at the point they are built.
func truncRunes(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// ---- global synthesis (v2): one cross-repo run over a workdir of bundles ----

const (
	// globalOutputFile is the workdir-relative file the v2 skill writes its
	// RawGlobalSynthesis to: the run's whole contract with the model's output,
	// and the only path the invocation permits Write against.
	globalOutputFile = "synthesis.json"
	// globalManifestFile names the evidence/asset manifest written beside the
	// bundle files; the skill reads it by this name.
	globalManifestFile = "manifest.md"
)

// DefaultGlobalTimeout bounds one global synthesis run, matching v1's per-repo
// ceiling: production recorded ~35 min per-repo runs with no tool use, and a
// global, tool-enabled run must not be killed after burning the spend.
const DefaultGlobalTimeout = 90 * time.Minute

// globalAllowedTools grants the nested claude the tools the v2 skill needs.
// Read/Glob/Grep are unqualified because the manifest points at assets outside
// the workdir. Write is granted for the one output file — a blanket Write
// denial would make the skill's central instruction unactionable — and the
// narrow grant is what stops any other file being written. Bash is constrained,
// not enforced (spec §Tool profile): the patterns cover the dotfiles `git log`
// the skill uses to date an existing rule, both plainly and in the proxied form
// a PreToolUse hook may rewrite a command into before the permission check sees
// it. A command outside the patterns is denied, not fatal: the skill degrades
// to "rule exists now", and rule recency is arbitrated by Go either way
// (verify2.go's gitRuleDate), never by the model.
var globalAllowedTools = []string{
	"Read", "Glob", "Grep",
	"Bash(git log:*)", "Bash(git -C:*)",
	"Bash(rtk git log:*)", "Bash(rtk git -C:*)",
	"Write(" + globalOutputFile + ")",
}

// globalDeniedTools bars rewriting the user's assets and fanning out spend.
// Write is deliberately absent: it is narrowed in globalAllowedTools, and a
// denial here would win over that grant.
var globalDeniedTools = []string{"Edit", "Agent"}

// GlobalSynthesizer produces one cross-repo synthesis from every repo's evidence
// bundle. Injected so the run pipeline is testable with a fake and no real LLM.
type GlobalSynthesizer interface {
	SynthesizeGlobal(ctx context.Context, bundles map[string]EvidenceBundle) (insights.RawGlobalSynthesis, error)
}

// GlobalSynthesizerFactory builds a run's GlobalSynthesizer once the run has
// materialized the skills into workDir — the nested claude's cwd, and the
// directory this synthesizer then fills with the bundle files and manifest.
// Only the run owns that directory's lifetime, hence a factory.
type GlobalSynthesizerFactory func(workDir string) GlobalSynthesizer

type claudeGlobalSynthesizer struct {
	cfg        insights.Config
	workDir    string
	globalRoot string // stands in for ~/.claude in the manifest
	run        func(ctx context.Context) (stdout []byte, err error)
}

// NewClaudeGlobalSynthesizer returns the production factory: one `claude -p`
// call under subscription auth, on cfg.SynthesisModel, in the run's workdir.
func NewClaudeGlobalSynthesizer(cfg insights.Config) GlobalSynthesizerFactory {
	return func(workDir string) GlobalSynthesizer {
		return NewClaudeGlobalSynthesizerPinned(cfg, "", workDir)
	}
}

// NewClaudeGlobalSynthesizerPinned is NewClaudeGlobalSynthesizer with the nested
// claude's config dir pinned too (see NewClaudeJudgePinned). An empty configDir
// leaves that knob inherited; workDir is always required.
func NewClaudeGlobalSynthesizerPinned(cfg insights.Config, configDir, workDir string) GlobalSynthesizer {
	// A pinned config dir *is* the ~/.claude the nested claude sees, so the
	// manifest must name that root: pointing it at the live one would have the
	// model read assets the run was deliberately isolated from.
	s := claudeGlobalSynthesizer{cfg: cfg, workDir: workDir, globalRoot: orDefault(configDir, globalAssetRoot())}
	s.run = func(ctx context.Context) ([]byte, error) {
		cmd, err := newGlobalSynthesizeCommand(ctx, cfg.SynthesisModel, configDir, workDir)
		if err != nil {
			return nil, err
		}
		out, err := cmd.Output()
		if err != nil {
			return out, wrapClaudeExit(out, err)
		}
		return out, nil
	}
	return s
}

// SynthesizeGlobal writes the bundles and manifest into the workdir, runs the
// single global claude call there, and decodes the synthesis.json the skill
// wrote. Verification is the caller's next step (VerifyGlobal), not this one's.
func (s claudeGlobalSynthesizer) SynthesizeGlobal(ctx context.Context, bundles map[string]EvidenceBundle) (insights.RawGlobalSynthesis, error) {
	var raw insights.RawGlobalSynthesis
	if s.workDir == "" {
		return raw, errors.New("global synthesis workDir is empty: the run must materialize the skills into a scratch cwd (skills.TempWorkdir)")
	}
	// Nothing to synthesize is a wiring bug, not an empty result: the due gate
	// decides whether a run happens, and a bundle-less call would spend a
	// 90-minute deadline on an empty workdir.
	if len(bundles) == 0 {
		return raw, errors.New("global synthesis has no bundles")
	}
	if _, err := WriteBundleFiles(s.workDir, bundles); err != nil {
		return raw, err
	}
	manifest := buildGlobalManifest(s.globalRoot, s.cfg, bundles)
	if err := atomicWrite(filepath.Join(s.workDir, globalManifestFile), []byte(manifest)); err != nil {
		return raw, fmt.Errorf("write %s: %w", globalManifestFile, err)
	}
	// An earlier call's output would be indistinguishable from this one's if the
	// CLI returns without writing.
	outPath := filepath.Join(s.workDir, globalOutputFile)
	if err := os.Remove(outPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return raw, err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultGlobalTimeout)
		defer cancel()
	}

	out, err := s.run(ctx)
	if err != nil {
		return raw, fmt.Errorf("global synthesis command: %w", err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		return raw, fmt.Errorf("global synthesis wrote no %s (claude: %s)", globalOutputFile, claudeResultText(out))
	}
	// json.Unmarshal accepts a bare null into a struct, which would present an
	// empty synthesis as a successful one.
	if body := bytes.TrimSpace(data); len(body) == 0 || bytes.Equal(body, []byte("null")) {
		return raw, fmt.Errorf("%s is empty", globalOutputFile)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return raw, fmt.Errorf("parse %s: %w", globalOutputFile, err)
	}
	return raw, nil
}

// newGlobalSynthesizeCommand builds the single claude invocation of a global
// run. Nothing is fed on stdin: the bundle files in the cwd are the input, which
// is what lets the model re-grep them. No --json-schema either — v2's payload is
// the file the skill writes, so validating a stdout structured output would make
// the model emit the whole synthesis twice into two sources of truth that can
// disagree. The skill's schema.json still reaches the workdir with the
// materialized skill, where SKILL.md points the model at it as the documented
// shape; Go re-checks that shape in VerifyGlobal.
func newGlobalSynthesizeCommand(ctx context.Context, model, configDir, workDir string) (*exec.Cmd, error) {
	if model == "" {
		return nil, errors.New("global synthesis model is empty: insights.LoadConfig defaults synthesis_model, and an unavailable model must fail the run rather than fall back")
	}
	if workDir == "" {
		return nil, errors.New("global synthesis workDir is empty: the run must materialize the skills into a scratch cwd (skills.TempWorkdir)")
	}
	cmd := exec.CommandContext(ctx, "claude", "-p", synthesisSkillCommand,
		"--output-format", "json",
		"--model", model,
		"--allowed-tools", strings.Join(globalAllowedTools, ","),
		"--disallowed-tools", strings.Join(globalDeniedTools, ","),
		// Nested synthesis calls must NOT persist their own session transcripts —
		// otherwise a re-run litters ~/.claude/projects with synthesizer exhaust
		// that then re-enters the scan as gated noise.
		"--no-session-persistence")
	// A context kill only signals the direct child; claude's own subprocesses
	// inherit the stdout pipe and can strand Output() long past the deadline.
	cmd.WaitDelay = 30 * time.Second
	cmd.Env = scrubbedEnv()
	if configDir != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+configDir)
	}
	cmd.Dir = workDir
	return cmd, nil
}

// globalAssetRoot is the operator's ~/.claude, where the global CLAUDE.md,
// settings and skills the manifest names live. An undeterminable home yields a
// relative ".claude", which simply finds nothing.
func globalAssetRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// buildGlobalManifest renders manifest.md: the evidence window as prose (the
// only dates the model may see), the bundle files with their session counts, and
// where every existing asset it may read lives. Absolute paths are safe here —
// the manifest lives and dies with the scratch workdir and is never stored.
// globalRoot stands in for ~/.claude so a frozen snapshot can be named instead.
func buildGlobalManifest(globalRoot string, cfg insights.Config, bundles map[string]EvidenceBundle) string {
	keys := make([]string, 0, len(bundles))
	for k := range bundles {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	files := make([]string, 0, len(keys))
	for _, k := range keys {
		files = append(files, fmt.Sprintf("%s-bundle.json (%d sessions)", k, bundles[k].SessionCount))
	}
	w := windowOf(bundles)

	var b strings.Builder
	b.WriteString("# Synthesis manifest\n")
	fmt.Fprintf(&b, "Window: %s → %s\n", orUnknown(w.From), orUnknown(w.To))
	fmt.Fprintf(&b, "Bundles (evidence, namespaced ids): %s\n", strings.Join(files, ", "))
	fmt.Fprintf(&b, "Existing assets (read-only): global CLAUDE.md %s; repo roots %s;\n",
		filepath.Join(globalRoot, "CLAUDE.md"), repoRootsFor(cfg, keys))
	fmt.Fprintf(&b, "skills dir %s; settings %s; dotfiles git history %s\n",
		filepath.Join(globalRoot, "skills"), filepath.Join(globalRoot, "settings.json"),
		orDefault(cfg.DotfilesRepo, "unavailable"))
	fmt.Fprintf(&b, "Write RawGlobalSynthesis JSON to ./%s\n", globalOutputFile)
	return b.String()
}

// repoRootsFor pairs each synthesized repo key with its configured checkout
// root, keying configured paths the way RepoKey does. A repo the config does
// not list simply has no root to read: the manifest names what exists.
func repoRootsFor(cfg insights.Config, keys []string) string {
	roots := make(map[string]string, len(cfg.Repos))
	for _, p := range cfg.Repos {
		roots[filepath.Base(p)] = p
	}
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		if p, ok := roots[k]; ok {
			pairs = append(pairs, k+": "+p)
		}
	}
	if len(pairs) == 0 {
		return "none configured"
	}
	return strings.Join(pairs, ", ")
}

func orUnknown(s string) string { return orDefault(s, "unknown") }

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// claudeResultText pulls the CLI's own report out of an --output-format json
// envelope, for the case where the run produced no output file (unknown model,
// "Not logged in", a refusal). Falls back to the raw output when the envelope
// does not parse; either way the text is capped before it reaches an error.
func claudeResultText(out []byte) string {
	var env struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err == nil && strings.TrimSpace(env.Result) != "" {
		return truncRunes(strings.TrimSpace(env.Result), 500)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return "no output"
	}
	return truncRunes(strings.TrimSpace(string(out)), 500)
}
