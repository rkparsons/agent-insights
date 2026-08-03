package insights

import (
	"context"
	"testing"

	"github.com/rkparsons/agent-insights/skills"
)

type fakeJudge struct {
	fields JudgedFields
	err    error
}

func (f fakeJudge) Judge(ctx context.Context, in ReducedInput) (JudgedFields, error) {
	return f.fields, f.err
}

// fixedJudge adapts a fake to the JudgeFactory seam: the run still materializes
// its skills workdir, the fake just has no use for it.
func fixedJudge(j Judge) JudgeFactory { return func(string) Judge { return j } }

// realJudge builds the production Judge for a manually gated real-claude test,
// delivering the skills into a scratch cwd exactly as a run does.
func realJudge(t *testing.T) Judge {
	t.Helper()
	dir := t.TempDir()
	if err := skills.Materialize(dir); err != nil {
		t.Fatal(err)
	}
	return NewClaudeJudge(dir)
}
