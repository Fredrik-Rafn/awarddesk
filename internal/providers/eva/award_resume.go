package eva

import (
	"context"
	"strings"
	"time"

	"awarddesk/internal/award"
)

func normalizedAwardRecoveryQuery(q award.Query) award.Query {
	q.Provider = strings.ToLower(strings.TrimSpace(q.Provider))
	q.Origin = strings.ToUpper(strings.TrimSpace(q.Origin))
	q.Destination = strings.ToUpper(strings.TrimSpace(q.Destination))
	q.Date = strings.TrimSpace(q.Date)
	q.Cabin = award.Cabin(strings.ToLower(strings.TrimSpace(string(q.Cabin))))
	q.Mode = strings.ToLower(strings.TrimSpace(q.Mode))
	q.Refresh = false
	return q
}

func reusableAwardQuote(ctx context.Context, q award.Query, choice awardChoice, now time.Time) (award.Fare, bool) {
	q = normalizedAwardRecoveryQuery(q)
	prior, ok := award.RecoveryResult(ctx)
	if !ok || q.Provider != "eva" || q.Mode != "award" || normalizedAwardRecoveryQuery(prior.Query) != q || prior.FetchedAt.IsZero() || prior.FetchedAt.After(now) || now.Sub(prior.FetchedAt) > 5*time.Minute || len(choice.itinerary.Segments) == 0 {
		return award.Fare{}, false
	}
	for _, s := range choice.itinerary.Segments {
		if canonicalAwardFlight(s.FlightNumber) == "" {
			return award.Fare{}, false
		}
	}
	for _, itinerary := range prior.Itineraries {
		if !sameAwardChoice(awardChoice{itinerary: itinerary}, choice) || len(itinerary.Fares) != 1 {
			continue
		}
		f := itinerary.Fares[0]
		if f.PricingType != "award" || f.Points <= 0 || f.Cabin != q.Cabin || f.AvailableFor != q.Adults || f.Estimated || f.MilesCash != nil || f.CashPrice != nil {
			continue
		}
		return f, true
	}
	return award.Fare{}, false
}
