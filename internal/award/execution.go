package award

import (
	"context"
	"time"
)

type executionObserverKey struct{}
type executionDeadlineKey struct{}

func WithExecutionObserver(ctx context.Context, started func()) context.Context {
	return context.WithValue(ctx, executionObserverKey{}, started)
}

func ExecutionStarted(ctx context.Context) {
	if started, ok := ctx.Value(executionObserverKey{}).(func()); ok {
		started()
	}
}

func WithExecutionDeadlineSource(ctx context.Context, deadline func() time.Time) context.Context {
	return context.WithValue(ctx, executionDeadlineKey{}, deadline)
}

func ExecutionDeadline(ctx context.Context) (time.Time, bool) {
	deadline, ok := ctx.Deadline()
	if source, exists := ctx.Value(executionDeadlineKey{}).(func() time.Time); exists && source != nil {
		if value := source(); !value.IsZero() && (!ok || value.Before(deadline)) {
			return value, true
		}
	}
	return deadline, ok
}
