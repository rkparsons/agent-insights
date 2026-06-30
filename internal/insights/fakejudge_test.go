package insights

import "context"

type fakeJudge struct {
	fields JudgedFields
	err    error
}

func (f fakeJudge) Judge(ctx context.Context, in ReducedInput) (JudgedFields, error) {
	return f.fields, f.err
}
