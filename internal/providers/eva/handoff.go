package eva

import (
	"bytes"
	"context"
	"errors"
	"net/url"

	"awarddesk/internal/award"
)

type searchRenewal struct{ cause error }

func (e *searchRenewal) Error() string { return e.cause.Error() }
func (e *searchRenewal) Unwrap() error { return e.cause }

func retrySearchPreparation(ctx context.Context, run func() (string, error), report award.Reporter) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		stage, err := run()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt >= 2 || !canRetrySearchRejection(stage, err) {
			return err
		}
		if report != nil {
			report(award.Progress{Stage: "search recovery", Message: "EVA rejected search preparation; automatically reopening the booking form with fresh verification", Recovery: recoveryDetail(stage, err, attempt+1)})
		}
	}
}

func canRetrySearchRejection(stage string, err error) bool {
	var failure *award.Error
	if !errors.As(err, &failure) || failure.Code != "session_denied" || failure.HTTPStatus != 403 || failure.OutcomeUnknown {
		return false
	}
	switch stage {
	case "flight search", "search captcha", "booking form security", "availability", "adaptive security", "booking handoff", "booking security", "booking CAPTCHA":
		return true
	}
	return false
}

func canRenewSearch(stage string, err error) bool {
	var failure *award.Error
	if !errors.As(err, &failure) || failure.OutcomeUnknown || (failure.Code != "connection" && failure.Code != "proxy_changed" && failure.Code != "response_read" && failure.Code != "session_expired") {
		return false
	}
	switch stage {
	case "flight search", "search captcha", "booking form security", "availability", "adaptive security", "booking handoff", "booking security", "booking CAPTCHA":
		return true
	}
	return false
}

type bookingSolver func(context.Context, string, []byte) error
type bookingForward func(context.Context, string, url.Values, string) ([]byte, string, error)

func completeBookingSecurity(ctx context.Context, body []byte, target string, solve bookingSolver, forward bookingForward, progress func(string, string)) ([]byte, string, error) {
	for attempt := 0; bytes.Contains(body, []byte("Pardon Our Interruption")) || bytes.Contains(body, []byte("reese84-resubmit-data")); attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, target, err
		}
		if !isReeseContinuation(body, target) {
			return nil, target, award.Failure("challenge_changed", "EVA booking security did not provide a supported continuation")
		}
		if attempt == 2 {
			return nil, target, award.Failure("challenge_rejected", "EVA booking security still rejected this session after two verification attempts")
		}
		f, _ := parseForm(body, target)
		progress("booking security", "Completing EVA booking security")
		if err := solve(ctx, target, body); err != nil {
			return nil, target, err
		}
		var err error
		body, target, err = forward(ctx, overrideURL, url.Values{"reese84-resubmit-data": {f.values.Get("reese84-resubmit-data")}}, target)
		if err != nil {
			return nil, target, err
		}
	}
	return body, target, nil
}
