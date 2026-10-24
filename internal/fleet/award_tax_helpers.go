package fleet

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"awarddesk/internal/award"
)

type awardTaxProvider interface {
	SearchAwardTaxEstimates(context.Context, award.Result, award.Reporter) (award.Result, error)
}

func awardTaxHelperBudget(ctx context.Context, now time.Time) time.Duration {
	budget := 90 * time.Second
	if deadline, ok := ctx.Deadline(); ok && deadline.Sub(now) < budget {
		budget = deadline.Sub(now)
	}
	if deadline, ok := award.ExecutionDeadline(ctx); ok && deadline.Sub(now) < budget {
		budget = deadline.Sub(now)
	}
	if budget < 10*time.Second {
		return 0
	}
	return budget
}

func (f *Fleet) awardTaxWorker(excluded map[string]bool) func(context.Context, award.Result, award.Reporter) (award.Result, error) {
	return func(ctx context.Context, original award.Result, report award.Reporter) (result award.Result, err error) {
		result = award.CloneResult(original)
		budget := awardTaxHelperBudget(ctx, time.Now())
		if budget == 0 {
			return result, nil
		}
		helpers := f.claimAwardHelpers(ctx, excluded, 1)
		if len(helpers) == 0 {
			return result, nil
		}
		helper := helpers[0]
		defer helper.release()
		provider, ok := helper.provider.(awardTaxProvider)
		if !ok {
			return result, nil
		}
		parent := ctx
		ctx, cancel := context.WithTimeout(ctx, budget)
		defer cancel()
		defer func() {
			if recover() != nil {
				err = award.Failure("internal", "Independent tax recovery stopped unexpectedly; award quotes are retained")
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) && parent.Err() == nil {
				err = award.Failure("award_tax_helper_timeout", "Independent tax recovery reached its time budget; award quotes are retained")
			}
			outcomeErr := err
			var failure *award.Error
			if errors.As(err, &failure) && (failure.Code == "award_helper_not_ready" || failure.Code == "award_tax_helper_timeout" || failure.Code == "session_expired" || failure.Code == "session_unverified") {
				outcomeErr = nil
			}
			f.outcome(helper.account.ID, helper.provider, outcomeErr, "award")
		}()
		slog.Info("EVA award tax recovery using an independent account", "origin", original.Query.Origin, "destination", original.Query.Destination, "date", original.Query.Date, "party", original.Query.Adults)
		return provider.SearchAwardTaxEstimates(ctx, award.CloneResult(original), report)
	}
}
