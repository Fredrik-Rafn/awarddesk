package session

import "context"

type retryObserverKey struct{}

func WithRetryObserver(ctx context.Context, observer func(error, int)) context.Context {
	return context.WithValue(ctx, retryObserverKey{}, observer)
}

func ReportRetry(ctx context.Context, err error, attempt int) {
	if observer, ok := ctx.Value(retryObserverKey{}).(func(error, int)); ok && observer != nil {
		observer(err, attempt)
	}
}
