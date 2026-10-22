package eva

import (
	"context"
	"errors"
	"sync"

	"awarddesk/internal/award"
	"awarddesk/internal/session"
)

func recoveryDetail(stage string, err error, attempt int) *award.Recovery {
	code := "provider_error"
	var failure *award.Error
	if errors.As(err, &failure) {
		code = failure.Code
		if failure.Stage != "" {
			stage = failure.Stage
		}
	}
	return &award.Recovery{Stage: stage, Code: code, Attempt: attempt}
}

// CAPTCHA polling and airline security preparation can report concurrently.
// Serialize both and attach retries to the currently active search stage.
func trackRequestRecoveries(ctx context.Context, observer award.Reporter) (context.Context, award.Reporter) {
	var mu sync.Mutex
	stage := "session preparation"
	send := func(p award.Progress) {
		stage = p.Stage
		if observer != nil {
			observer(p)
		}
	}
	report := func(p award.Progress) { mu.Lock(); defer mu.Unlock(); send(p) }
	ctx = session.WithRetryObserver(ctx, func(err error, attempt int) {
		mu.Lock()
		defer mu.Unlock()
		send(award.Progress{Stage: stage, Message: "A read-only request was interrupted; retrying automatically", Recovery: recoveryDetail(stage, err, attempt)})
	})
	return ctx, report
}
