package eva

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/session"
)

type savedSessionOnlyKey struct{}

func savedSessionOnly(ctx context.Context) bool {
	value, _ := ctx.Value(savedSessionOnlyKey{}).(bool)
	return value
}

func (p *Provider) searchAwardWithTaxes(ctx context.Context, q award.Query, report award.Reporter) (award.Result, error) {
	r, err := p.searchAward(ctx, q, report)
	if err != nil || !r.Complete || len(r.Itineraries) == 0 {
		return r, err
	}
	if !needsAwardTaxLookup(r) {
		p.convertAwardTaxes(ctx, &r)
		return r, ctx.Err()
	}
	taxCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	taxCtx = context.WithValue(taxCtx, savedSessionOnlyKey{}, true)
	taxQuery := q
	taxQuery.Mode, taxQuery.Refresh = "", true

	taxReport := func(pr award.Progress) {
		pr.Stage = "award tax estimates"
		pr.Message = "Retrieving itemized cash-fare charges for matching award flights"
		pr.Preview = &r
		if report != nil {
			report(pr)
		}
	}
	taxErr := retryAwardTaxLookup(taxCtx, &r, func() error {
		_, err := p.searchCashWithPricing(taxCtx, taxQuery, taxReport, func(ctx context.Context, c *session.Client, page pageData, cash *award.Result, innerReport award.Reporter) error {
			awardProgress(innerReport, "award tax estimates", "Retrieving tax estimates", 0, len(r.Itineraries), &r)
			return collectAwardTaxEstimates(ctx, c, page, *cash, &r, innerReport)
		})
		return err
	}, taxReport)
	if ctx.Err() != nil {
		return r, ctx.Err()
	}
	if taxErr != nil {
		code := "unavailable"
		var failure *award.Error
		if errors.As(taxErr, &failure) {
			code = failure.Code
		}
		slog.Warn("EVA award tax estimates incomplete; retaining award quotes", "code", code,
			"origin", q.Origin, "destination", q.Destination, "date", q.Date)
	}
	r = recoverAwardTaxEstimates(taxCtx, r, report)
	p.convertAwardTaxes(ctx, &r)
	if ctx.Err() != nil {
		return r, ctx.Err()
	}
	awardProgress(report, "award tax estimates", "Award mileage is ready; available tax estimates are labelled separately", 0, 0, &r)
	return r, nil
}

func (p *Provider) convertAwardTaxes(ctx context.Context, result *award.Result) {
	ctx, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	for x := range result.Itineraries {
		for y := range result.Itineraries[x].Fares {
			f := &result.Itineraries[x].Fares[y]
			if f.Taxes.Amount != "" && f.TaxesUSD == nil {
				f.TaxesUSD, _ = p.config.Exchange.USD(ctx, f.Taxes)
			} else if f.TaxEstimate != nil && f.TaxEstimate.USD == nil {
				f.TaxEstimate.USD, _ = p.config.Exchange.USD(ctx, f.TaxEstimate.Total)
			}
		}
	}
}

func retryAwardTaxLookup(ctx context.Context, result *award.Result, lookup func() error, report award.Reporter) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := lookup()
		if err == nil || attempt == 1 || ctx.Err() != nil || !needsAwardTaxLookup(*result) || !canRetryAwardTaxLookup(err) {
			return err
		}
		if report != nil {
			report(award.Progress{Stage: "award tax estimates", Message: "Refreshing the tax quotation context; completed estimates are retained", Recovery: recoveryDetail("award tax estimates", err, attempt+1), Preview: result})
		}
	}
}

func canRetryAwardTaxLookup(err error) bool {
	var failure *award.Error
	if !errors.As(err, &failure) || failure.OutcomeUnknown {
		return false
	}
	switch failure.Stage {
	case "credentials", "otp", "otp submission", "verification continuation", "mailbox":
		return false
	}
	switch failure.Code {
	case "connection", "response_read", "search_protocol", "pricing_protocol", "session_denied", "challenge_rejected", "challenge_service", "site_unavailable":
		return true
	case "provider_http":
		return failure.HTTPStatus >= 500 && failure.HTTPStatus <= 599
	}
	return false
}

func needsAwardTaxLookup(r award.Result) bool {
	for _, item := range r.Itineraries {
		if !homogeneousAwardTaxCabin(item, r.Query.Cabin) {
			continue
		}
		for _, f := range item.Fares {
			if f.PricingType == "award" && f.Taxes.Amount == "" && f.TaxEstimate == nil {
				return true
			}
		}
	}
	return false
}

func homogeneousAwardTaxCabin(item award.Itinerary, cabin award.Cabin) bool {
	if len(item.Segments) == 0 {
		return false
	}
	for _, s := range item.Segments {
		if s.Cabin != cabin {
			return false
		}
	}
	return true
}

func collectAwardTaxEstimates(ctx context.Context, c pricingClient, page pageData, cash award.Result, result *award.Result, report award.Reporter) error {
	initial := page
	selected := false
	for x := range result.Itineraries {
		item := &result.Itineraries[x]
		if !homogeneousAwardTaxCabin(*item, result.Query.Cabin) {
			continue
		}
		pending := false
		for _, f := range item.Fares {
			pending = pending || (f.PricingType == "award" && f.Taxes.Amount == "" && f.TaxEstimate == nil)
		}
		if !pending {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if selected {
			var err error
			page, cash, err = refreshAvailability(ctx, c, page, initial, cash.Query)
			if err != nil {
				return err
			}
			selected = false
		}
		flight, fare, found := cashTaxCandidate(result.Query, *item, cash)
		if !found {
			logAwardTaxIssue(result.Query, *item, "no exact cash itinerary match")
			continue
		}
		if estimate, ok := cashAvailabilityTaxEstimate(page, flight, fare, result.Query.Adults, time.Now().UTC()); ok {
			attachAwardTaxEstimate(item, estimate)
			awardProgress(report, "award tax estimates", "Cash availability tax estimate retrieved for a matching flight", x+1, len(result.Itineraries), result)
			continue
		}
		v := ajaxContext(page)
		v.Set("FLIGHT_ID_1", flight.ID)
		v.Set("RECOMMENDATION_ID_1", fare.QuoteID)
		next, err := quoteRequest(ctx, c, page.SessionID, "fare", v)
		if err != nil {
			return err
		}
		page, err = advancePage(page, next)
		if err != nil {
			return err
		}
		selected = true
		if err := validateCashTaxSelection(page.Business["ListItineraryView"], *item, result.Query); err != nil {
			logAwardTaxIssue(result.Query, *item, err.Error())
			continue
		}
		estimate, err := parseCashTaxEstimate(page.Business["Price"], result.Query.Adults, time.Now().UTC())
		if err != nil {

			logAwardTaxIssue(result.Query, *item, err.Error())
			continue
		}
		if estimate.Total.Currency != fare.CashPrice.Currency {
			logAwardTaxIssue(result.Query, *item, "cash tax currency changed")
			continue
		}
		attachAwardTaxEstimate(item, estimate)
		awardProgress(report, "award tax estimates", "Cash-derived tax estimate retrieved for a matching flight", x+1, len(result.Itineraries), result)
	}
	return nil
}

func attachAwardTaxEstimate(item *award.Itinerary, estimate *award.TaxEstimate) {
	for y := range item.Fares {
		f := &item.Fares[y]
		if f.PricingType == "award" && f.Taxes.Amount == "" && f.TaxEstimate == nil {
			copy := *estimate
			copy.Components = append([]award.TaxComponent(nil), estimate.Components...)
			f.TaxEstimate = &copy
		}
	}
}

func logAwardTaxIssue(q award.Query, item award.Itinerary, reason string) {
	var flights []string
	for _, s := range item.Segments {
		flights = append(flights, s.FlightNumber)
	}
	slog.Warn("EVA award tax estimate omitted", "flights", awardFlightKey(flights), "reason", reason,
		"origin", q.Origin, "destination", q.Destination, "date", q.Date, "party", q.Adults)
}
