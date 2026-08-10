// Command agent-insights is the standalone CLI for the agent-insights
// pipeline: analyze Claude Code sessions, synthesize repo-level insight
// documents, and score the pipeline itself via the eval subcommand.
package main

import (
	"fmt"
	"os"

	"github.com/rkparsons/agent-insights/cmd"
)

const usage = "usage: agent-insights backfill|analyze|synthesize|enrich|status|show|acted|unacted|eval ..."

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	// RunInsights already special-cases args[0] == "eval" by delegating to
	// RunInsightsEval, so a single call reaches every subcommand — including
	// eval — exactly as `tmux-ctrl insights <subcommand> ...` used to. Both
	// functions handle their own errors (print + os.Exit) rather than
	// returning one.
	cmd.RunInsights(os.Args[1:])
}
