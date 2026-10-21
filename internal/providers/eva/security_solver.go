package eva

import (
	"awarddesk/internal/award"
	"context"
	"errors"
)

// Only upstream generation failures permit changing solver. Request failures
// through the airline proxy and unknown provider responses retain their normal
// recovery paths. No login, OTP, booking handoff or pricing request is replayed.
func reeseWithFallback(ctx context.Context, primary, backup func(context.Context) error, progress func(string, string)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := primary(ctx)
	var failure *award.Error
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !errors.As(err, &failure) || failure.Code != "challenge_service" || failure.OutcomeUnknown {
		return err
	}
	progress("booking security", "The security service is unavailable; trying the backup service")
	return backup(ctx)
}
