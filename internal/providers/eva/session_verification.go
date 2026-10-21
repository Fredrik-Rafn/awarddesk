package eva

import (
	"awarddesk/internal/award"
	"context"
	"errors"
)

// Only used before credentials/OTP, or for the saved-session read-only GET.
// Never apply this classification to credential, OTP or pricing submissions.
func canRecoverSessionSetup(err error) bool {
	var failure *award.Error
	if !errors.As(err, &failure) || failure.OutcomeUnknown {
		return false
	}
	return proxyNeedsRenewal(err) || (failure.Code == "session_denied" && failure.HTTPStatus == 403)
}

func verifySavedSession(ctx context.Context, get func() ([]byte, string, error), report award.Reporter) ([]byte, string, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		body, target, err := get()
		if !canRecoverSessionSetup(err) || ctx.Err() != nil {
			return body, target, err
		}
		if attempt == 1 {
			return nil, target, &searchRenewal{cause: err}
		}
		if report != nil {
			report(award.Progress{Stage: "session verification retry", Message: "EVA interrupted the saved-session check; retrying verification automatically", Recovery: recoveryDetail("session verification", err, attempt+1)})
		}
	}
}
