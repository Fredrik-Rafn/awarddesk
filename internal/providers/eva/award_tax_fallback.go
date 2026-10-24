package eva

import (
	"context"
	"log/slog"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/session"
)

type AwardTaxWorker func(context.Context, award.Result, award.Reporter) (award.Result, error)

type awardTaxWorkerKey struct{}

func WithAwardTaxWorker(ctx context.Context, worker AwardTaxWorker) context.Context {
	return context.WithValue(ctx, awardTaxWorkerKey{}, worker)
}

func (p *Provider) SearchAwardTaxEstimates(ctx context.Context, original award.Result, report award.Reporter) (award.Result, error) {
	r := award.CloneResult(original)
	if r.Query.Mode != "award" || !r.Complete || !needsAwardTaxLookup(r) {
		return r, award.Failure("unsupported", "Award tax recovery requires completed award quotes with missing estimates")
	}
	if !p.SessionInfo().Ready {
		return r, award.Failure("award_helper_not_ready", "Independent tax recovery requires an existing ready member session")
	}
	ctx = context.WithValue(ctx, savedSessionOnlyKey{}, true)
	ctx = WithAwardTaxWorker(ctx, nil)
	ctx = WithAwardQuoteWorkerFactory(ctx, nil)
	q := r.Query
	q.Mode, q.Refresh = "", true
	_, err := p.searchCashWithPricing(ctx, q, report, func(ctx context.Context, c *session.Client, page pageData, cash *award.Result, report award.Reporter) error {
		return collectAwardTaxEstimates(ctx, c, page, *cash, &r, report)
	})
	return r, err
}

func recoverAwardTaxEstimates(ctx context.Context, original award.Result, report award.Reporter) award.Result {
	worker, _ := ctx.Value(awardTaxWorkerKey{}).(AwardTaxWorker)
	if worker == nil || ctx.Err() != nil || !original.Complete || original.Query.Mode != "award" || !needsAwardTaxLookup(original) {
		return original
	}
	original = award.CloneResult(original)
	child, err := worker(ctx, award.CloneResult(original), func(pr award.Progress) {
		if report != nil && pr.Recovery != nil {
			recovery := *pr.Recovery
			report(award.Progress{Stage: "award tax estimates", Message: "An independent account recovered while retrieving tax estimates", Recovery: &recovery, Preview: &original})
		}
	})
	accepted := mergeAwardTaxEstimates(&original, child, time.Now().UTC())
	if err != nil && ctx.Err() == nil && report != nil {
		report(award.Progress{Stage: "award tax estimates", Message: "Independent tax recovery was incomplete; verified award quotes are retained", Recovery: recoveryDetail("award tax estimates", err, 1), Preview: &original})
	}
	slog.Info("EVA independent award tax recovery finished", "estimates", accepted, "incomplete", needsAwardTaxLookup(original), "origin", original.Query.Origin, "destination", original.Query.Destination, "date", original.Query.Date, "party", original.Query.Adults)
	return original
}

func mergeAwardTaxEstimates(result *award.Result, child award.Result, now time.Time) int {
	if normalizedAwardRecoveryQuery(result.Query) != normalizedAwardRecoveryQuery(child.Query) {
		return 0
	}
	child = award.CloneResult(child)
	accepted := 0
	for i := range result.Itineraries {
		item := &result.Itineraries[i]
		if !homogeneousAwardTaxCabin(*item, result.Query.Cabin) {
			continue
		}
		for _, other := range child.Itineraries {
			if !sameAwardChoice(awardChoice{itinerary: *item}, awardChoice{itinerary: other}) {
				continue
			}
			for j := range item.Fares {
				fare := &item.Fares[j]
				if fare.PricingType != "award" || fare.Taxes.Amount != "" || fare.TaxEstimate != nil {
					continue
				}
				for _, candidate := range other.Fares {
					if candidate.PricingType != "award" || candidate.Points != fare.Points || candidate.Cabin != fare.Cabin || candidate.AvailableFor != fare.AvailableFor || candidate.Estimated || candidate.MilesCash != nil || candidate.CashPrice != nil || !validRecoveredTaxEstimate(candidate.TaxEstimate, now) {
						continue
					}
					fare.TaxEstimate = candidate.TaxEstimate
					accepted++
					break
				}
			}
		}
	}
	return accepted
}

func validRecoveredTaxEstimate(e *award.TaxEstimate, now time.Time) bool {
	if e == nil || (e.Source != "eva_cash_fare" && e.Source != "eva_cash_availability") || e.QuotedAt.IsZero() || e.QuotedAt.After(now) || now.Sub(e.QuotedAt) > 5*time.Minute || !cashTaxCurrency.MatchString(e.Total.Currency) {
		return false
	}
	_, err := partyCashAmount(e.Total.Amount, 1)
	return err == nil
}
