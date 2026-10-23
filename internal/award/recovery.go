package award

import "context"

type recoveryResultKey struct{}

func WithRecoveryResult(ctx context.Context, r Result) context.Context {
	return context.WithValue(ctx, recoveryResultKey{}, CloneResult(r))
}

func RecoveryResult(ctx context.Context) (Result, bool) {
	r, ok := ctx.Value(recoveryResultKey{}).(Result)
	if !ok {
		return Result{}, false
	}
	return CloneResult(r), true
}
