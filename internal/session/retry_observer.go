package session

import "context"

type retryObserverKey struct{}

// WithRetryObserver records a failed read-only request before its bounded
// retry. It exposes sanitized errors only, never upstream payloads or URLs.
func WithRetryObserver(ctx context.Context, observer func(error, int)) context.Context {
	return context.WithValue(ctx, retryObserverKey{}, observer)
}

// ReportRetry notifies the observer about a sanitized failed read-only attempt.
func ReportRetry(ctx context.Context, err error, attempt int) {
	if observer, ok := ctx.Value(retryObserverKey{}).(func(error, int)); ok && observer != nil {
		observer(err, attempt)
	}
}
