package eva

import (
	"context"
	"errors"

	"awarddesk/internal/award"
)

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
