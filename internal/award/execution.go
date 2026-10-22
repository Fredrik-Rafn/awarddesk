package award

import "context"

type executionObserverKey struct{}

// WithExecutionObserver lets a scheduler separate capacity wait from actual
// airline work. Providers with account scheduling call ExecutionStarted once
// they own an account, before making its first external request.
func WithExecutionObserver(ctx context.Context, started func()) context.Context {
	return context.WithValue(ctx, executionObserverKey{}, started)
}

func ExecutionStarted(ctx context.Context) {
	if started, ok := ctx.Value(executionObserverKey{}).(func()); ok {
		started()
	}
}
