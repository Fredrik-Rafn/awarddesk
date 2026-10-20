package eva

import (
	"awarddesk/internal/award"
	"bytes"
	"context"
	"errors"
	"net/url"
)

type searchRenewal struct{ cause error }

func (e *searchRenewal) Error() string { return e.cause.Error() }
func (e *searchRenewal) Unwrap() error { return e.cause }

func canRenewSearch(stage string, err error) bool {
	var failure *award.Error
	if !errors.As(err, &failure) || (failure.Code != "connection" && failure.Code != "proxy_changed") {
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

// Only continue a request explicitly intercepted by Imperva. Never replay the
// original SSO request, a credential submission, or a pricing operation here.
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
