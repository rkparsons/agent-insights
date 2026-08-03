package insightseval

import (
	"path/filepath"
	"testing"
)

// writeMinimalRubricSet seeds <dataDir>/rubrics with a small synthetic set
// covering every id the pipeline touches by hardcoded name: the probe triad
// RunProbes reads directly (C-01 recall, N-01 negative_recall, C-04
// near_miss — see probes.go's probeRecallRubricID etc.), a gap target
// scorerun tests script matcher responses for (M1, repos[0]=tmux-ctrl so it
// sees the fixture bucket's items), and an always-absent HIGH must_pass
// regression target (C-07) whose benchmark.json status entry scorerun tests
// edit directly. No real session ids, no real employer repos — this
// replaces what used to be transparently available via the go:embed rubric
// set now that rubrics live in the private data repo.
func writeMinimalRubricSet(t *testing.T, dataDir string) {
	t.Helper()
	rubrics := map[string]string{
		"C-01.yaml": `id: C-01
part: regression
tier: HIGH
surface: either
repos: [alpha]
statement: synthetic statement for the recall probe.
required_nuances:
  - synthetic nuance
`,
		"C-04.yaml": `id: C-04
part: regression
tier: MODERATE
surface: either
repos: [alpha]
statement: synthetic statement for the near-miss probe.
required_nuances:
  - synthetic nuance
forbidden_generalizations:
  - synthetic forbidden generalization
`,
		"C-07.yaml": `id: C-07
part: regression
tier: HIGH
surface: either
repos: [beta]
statement: synthetic statement, always absent from the tmux-ctrl fixture bucket.
required_nuances:
  - synthetic nuance
`,
		"M1.yaml": `id: M1
part: gap
tier: HIGH
surface: either
repos: [tmux-ctrl, alpha]
statement: synthetic gap statement.
required_nuances:
  - synthetic nuance one
  - synthetic nuance two
`,
		"N-01.yaml": `id: N-01
part: negative
statement: synthetic negative statement (forbidden-form probe).
forbidden_generalizations:
  - synthetic forbidden generalization
`,
	}
	dir := filepath.Join(dataDir, "rubrics")
	for name, content := range rubrics {
		mustWriteFile(t, filepath.Join(dir, name), content)
	}
}

// writeGapHeavyRubricSet seeds <dataDir>/rubrics with six gap targets
// (M1..M6, defaulting to expected_fail — SeedStatuses's ratchet-survives
// check needs several missing statuses to fill: M1 arrives pre-seeded, so
// M2..M6 are the 5 it must add) plus one negative (N-01, which must never
// get a status). Used only by the SeedStatuses unit test.
func writeGapHeavyRubricSet(t *testing.T, dataDir string) {
	t.Helper()
	dir := filepath.Join(dataDir, "rubrics")
	for _, id := range []string{"M1", "M2", "M3", "M4", "M5", "M6"} {
		mustWriteFile(t, filepath.Join(dir, id+".yaml"), "id: "+id+`
part: gap
tier: HIGH
surface: either
repos: [alpha]
statement: synthetic gap statement.
`)
	}
	mustWriteFile(t, filepath.Join(dir, "N-01.yaml"), `id: N-01
part: negative
statement: synthetic negative statement.
forbidden_generalizations:
  - synthetic forbidden generalization
`)
}
