package synthesis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/skills"
)

type Options struct {
	MinSessions int           // 0 = the config's min_sessions
	DryRun      bool          // report bundle sizes and due reasoning, spend nothing
	Due         bool          // run only if the global run is due
	Timeout     time.Duration // 0 = DefaultGlobalTimeout
	LogPath     string        // recorded in the run state; the spawner tees output here
}

// Summary reports what one global run did. Repos counts the bundles that would
// feed the model; Written is 0 or 1 — a run stores one cross-repo snapshot or
// none at all.
type Summary struct {
	Repos   int
	Written int
	Due     bool
	// Contributing lists the repos with sessions analyzed since the last
	// snapshot, whether or not the run was due (see GlobalDue).
	Contributing []string
}

// RunSynthesize performs one global synthesis: build every qualifying repo's
// bundle, run the single cross-repo model call over them, verify the result in
// Go, and store one snapshot. Any failure — model, verifier, privacy scan —
// stores nothing and lands its reason verbatim in the run state's error, which
// is what `status --json` surfaces as last_run.error.
func RunSynthesize(ctx context.Context, newSyn GlobalSynthesizerFactory, opts Options) (sum Summary, retErr error) {
	cfg, err := insights.LoadConfig()
	if err != nil {
		return Summary{}, err
	}
	cfg.WarnIfNoRepos()
	if opts.MinSessions > 0 {
		// One threaded value: GroupByRepo here and inside GlobalDue must agree
		// on the floor, or a repo could be due without being bundled.
		cfg.MinSessions = opts.MinSessions
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultGlobalTimeout
	}

	// A store with no analyses yet is a valid empty state, not an error:
	// RunSynthesize must still reach the run-state write below so
	// due/running/error stays visible to the TUI.
	analyses, err := LoadAnalyses()
	if err != nil && !os.IsNotExist(err) {
		return Summary{}, err
	}
	latest, hasLatest, err := LoadLatestGlobal()
	if err != nil {
		return Summary{}, err
	}
	var lastGenerated time.Time
	if hasLatest {
		lastGenerated = latest.GeneratedAt
	}
	due, contributing := GlobalDue(analyses, cfg, lastGenerated, time.Now())

	groups := GroupByRepo(analyses, cfg.MinSessions, cfg)
	bundles := buildBundles(groups)
	sum = Summary{Repos: len(bundles), Due: due, Contributing: contributing}

	if opts.DryRun {
		reportDryRun(bundles, sum, cfg, lastGenerated)
		return sum, nil
	}
	if opts.Due && !due {
		fmt.Fprintf(os.Stderr, "synthesis: not due (%s)\n", dueReason(sum, cfg, lastGenerated))
		return sum, nil
	}
	if len(bundles) == 0 {
		// Nothing clears the bundle floor: there is nothing to synthesize, and
		// spending a 90-minute deadline on an empty workdir would be worse than
		// reporting it. Still records a run state.
		fmt.Fprintf(os.Stderr, "synthesis: no repo meets the %d-session floor\n", cfg.MinSessions)
	}

	lock, err := insights.AcquireLock("synthesize")
	if err != nil {
		return sum, err
	}
	defer lock.Release()

	rs := RunState{Status: "running", PID: os.Getpid(), StartedAt: time.Now().UTC(), LogPath: opts.LogPath}
	writeRunState(rs)
	defer func() {
		finishedAt := time.Now().UTC()
		rs.FinishedAt = &finishedAt
		rs.Written = sum.Written
		rs.Status = "ok"
		if retErr != nil {
			rs.Status, rs.Reason = "failed", retErr.Error()
		}
		writeRunState(rs)
	}()

	if len(bundles) == 0 {
		return sum, nil
	}
	if newSyn == nil {
		return sum, errors.New("no synthesizer factory: a run must be able to build its synthesizer for the materialized workdir")
	}
	// Skill delivery is the run's job, not the operator's ~/.claude: the nested
	// claude call works out of a scratch cwd with the skills materialized into
	// it, removed when the run ends. Set up after the run-state defer so a
	// failure here is recorded like any other.
	workDir, cleanupWorkDir, err := skills.TempWorkdir()
	if err != nil {
		return sum, err
	}
	defer cleanupWorkDir()

	rctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	raw, err := newSyn(workDir).SynthesizeGlobal(rctx, bundles)
	if err != nil {
		return sum, withPreservedOutput(workDir, err)
	}
	// Verification runs under the run's own context, not the model's: a call
	// that used its whole deadline would otherwise start verification already
	// expired, silently skipping the git-dated recency arbitration.
	snapshot, err := VerifyGlobal(ctx, raw, bundles, cfg, time.Now().UTC())
	if err != nil {
		return sum, withPreservedOutput(workDir, err)
	}
	path, err := StoreGlobal(snapshot)
	if err != nil {
		// Verified but unstored: the model output is still the expensive half.
		return sum, withPreservedOutput(workDir, err)
	}
	sum.Written = 1
	if n := len(snapshot.Meta.ValidationNotes); n > 0 {
		fmt.Fprintf(os.Stderr, "synthesis: %d validation note(s) recorded in the snapshot\n", n)
	}
	fmt.Fprintf(os.Stderr, "synthesis: wrote %s (%d findings, %d dropped)\n", path, len(snapshot.Findings), len(snapshot.Dropped))
	return sum, nil
}

// buildBundles turns each qualifying group into its evidence bundle, keyed by
// the same repo key BuildBundle stamps into Repo — the id namespacing, the
// verifier's item index, and the manifest all key off that one string.
func buildBundles(groups map[string][]insights.AgentSessionAnalysis) map[string]EvidenceBundle {
	bundles := make(map[string]EvidenceBundle, len(groups))
	for key, group := range groups {
		bundles[key] = BuildBundle(key, group)
	}
	return bundles
}

// withPreservedOutput copies the failed run's model output out of the scratch
// workdir and names the copy in the returned error. A 90-minute run that fails
// verification leaves nothing else behind, and the error text is what reaches
// last_run.error — so the post-mortem path has to travel with it.
func withPreservedOutput(workDir string, cause error) error {
	path, err := preserveFailedSynthesis(workDir, time.Now().UTC())
	switch {
	case err != nil:
		return fmt.Errorf("%w (model output could not be preserved: %v)", cause, err)
	case path == "":
		return cause
	default:
		return fmt.Errorf("%w (model output preserved at %s)", cause, path)
	}
}

// reportDryRun prints what a real run would feed the model and why it would or
// would not start. It spends nothing: bundles are pure Go.
func reportDryRun(bundles map[string]EvidenceBundle, sum Summary, cfg insights.Config, lastGenerated time.Time) {
	keys := make([]string, 0, len(bundles))
	for k := range bundles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b := bundles[k]
		fmt.Fprintf(os.Stderr, "synthesis (dry-run): %s · %d analyses · %.1f KB · friction %d · prefs %d · success %d · signals %d\n",
			k, b.AnalyzedCount, bundleKB(k, b), len(b.Friction), len(b.Prefs), len(b.Success), len(b.Signals))
	}
	fmt.Fprintf(os.Stderr, "synthesis (dry-run): due=%v — %s\n", sum.Due, dueReason(sum, cfg, lastGenerated))
}

// bundleKB is a bundle's marshaled size, the number that matters for the
// model's input budget. A bundle that cannot marshal reports 0 — this is a
// diagnostic line, never a gate.
func bundleKB(repo string, b EvidenceBundle) float64 {
	data, err := marshalBundleFile(repo, b)
	if err != nil {
		return 0
	}
	return float64(len(data)) / 1024
}

// dueReason states the due decision in the two terms that make it: elapsed
// cadence and new analyzed sessions since the last snapshot.
func dueReason(sum Summary, cfg insights.Config, lastGenerated time.Time) string {
	age := "no prior snapshot"
	if !lastGenerated.IsZero() {
		age = fmt.Sprintf("last snapshot %d day(s) ago, cadence %d", int(time.Since(lastGenerated).Hours()/24), cfg.CadenceDays)
	}
	contributing := "none"
	if len(sum.Contributing) > 0 {
		contributing = strings.Join(sum.Contributing, ", ")
	}
	return fmt.Sprintf("%s; new sessions from: %s; threshold %d", age, contributing, cfg.DueNewSessions)
}
