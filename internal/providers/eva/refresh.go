package eva

import (
	"awarddesk/internal/award"
	"awarddesk/internal/session"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Reopen flight selection in the current authenticated booking session. These
// are the fields EVA's own modify-search builder sends; no CAPTCHA is replayed.
func refreshAvailability(ctx context.Context, c pricingClient, current, initial pageData, q award.Query) (pageData, award.Result, error) {
	v := ajaxContext(current)
	for _, k := range []string{"B_LOCATION_1", "E_LOCATION_1", "B_DATE_1", "B_ANY_TIME_1", "DISPLAY_TYPE", "ARRANGE_BY", "PREF_AIR_FREQ_AIRLINE_1_1", "PREF_AIR_FREQ_LEVEL_1_1", "PREF_AIR_FREQ_MILES_1_1", "PREF_AIR_FREQ_NUMBER_1_1"} {
		if value, ok := initial.Basefacts["request."+k]; ok {
			v.Set(k, value)
		}
	}
	for n := 1; n <= 12; n++ {
		k := "COMMERCIAL_FARE_FAMILY_" + strconv.Itoa(n)
		if value, ok := initial.Basefacts["request."+k]; ok {
			v.Set(k, value)
		}
	}
	v.Set("B_LOCATION_1", q.Origin)
	v.Set("E_LOCATION_1", q.Destination)
	v.Set("B_DATE_1", strings.ReplaceAll(q.Date, "-", "")+"0000")
	v.Set("B_ANY_TIME_1", "TRUE")
	v.Set("DATE_RANGE_VALUE_1", "0")
	adults := q.Adults
	if adults < 1 {
		adults = 1
	}
	for i := 1; i <= adults; i++ {
		n := strconv.Itoa(i)
		v.Set("TRAVELLER_TYPE_"+n, "ADT")
		v.Set("HAS_INFANT_"+n, "FALSE")
	}
	var cfg struct{ DateRangeQualifier string }
	if json.Unmarshal(initial.Config["FPOW"], &cfg) == nil && cfg.DateRangeQualifier != "" {
		v.Set("DATE_RANGE_QUALIFIER_1", cfg.DateRangeQualifier)
	}
	var next pageData
	var err error
	for attempt := 0; ; attempt++ {
		next, err = quoteRequest(ctx, c, current.SessionID, "refresh availability", v)
		if ctx.Err() != nil {
			return current, award.Result{}, ctx.Err()
		}
		if attempt == 1 || !canRetryAvailabilityRead(err) {
			break
		}
		session.ReportRetry(ctx, err, attempt+1)
		if err = session.Pause(ctx, 2*time.Second); err != nil {
			return current, award.Result{}, err
		}
	}
	if err != nil {
		return current, award.Result{}, err
	}
	next, err = advancePage(current, next)
	if err != nil {
		return current, award.Result{}, err
	}
	r, err := parseAvailability(next.Business["Availability"], q)
	return next, r, err
}

// This POST only reopens flight availability. A bounded retry is safe even if
// its response was lost, and never repeats fare selection or ACTION=BOOK.
func canRetryAvailabilityRead(err error) bool {
	var failure *award.Error
	if !errors.As(err, &failure) {
		return false
	}
	return failure.Code == "connection" || failure.Code == "response_read" || failure.Code == "search_protocol" ||
		(failure.Code == "provider_http" && failure.HTTPStatus >= 500 && failure.HTTPStatus <= 599)
}

func matchingSelection(fresh award.Result, wanted award.Itinerary, fare award.Fare) (string, string, error) {
	for _, i := range fresh.Itineraries {
		if len(i.Segments) != len(wanted.Segments) {
			continue
		}
		match := true
		for n, s := range i.Segments {
			w := wanted.Segments[n]
			if s.FlightNumber != w.FlightNumber || s.Origin != w.Origin || s.Destination != w.Destination || s.DepartureLocal != w.DepartureLocal {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		for _, f := range i.Fares {
			if f.Name == fare.Name && f.Cabin == fare.Cabin && f.CashPrice != nil && fare.CashPrice != nil && f.CashPrice.Currency == fare.CashPrice.Currency && f.CashPrice.Amount == fare.CashPrice.Amount {
				return i.ID, f.QuoteID, nil
			}
		}
	}
	return "", "", award.Failure("price_changed", "EVA changed flight availability or fare prices during pricing; run a fresh search")
}

// A refreshed snapshot owns its identifiers and prices. Carry a quote forward
// only when its flight segments, fare family, cabin and cash amount still match.
// Never copy old recommendation IDs into the refreshed result.
func preserveMatchingQuotes(fresh *award.Result, previous award.Result) {
	for x := range fresh.Itineraries {
		for y := range fresh.Itineraries[x].Fares {
			fare := &fresh.Itineraries[x].Fares[y]
			if fare.MilesCashEligible != nil && !*fare.MilesCashEligible {
				continue
			}
			for _, old := range previous.Itineraries {
				for _, priced := range old.Fares {
					if priced.MilesCash == nil {
						continue
					}
					candidate := award.Result{Itineraries: []award.Itinerary{{ID: fresh.Itineraries[x].ID, Segments: fresh.Itineraries[x].Segments, Fares: []award.Fare{*fare}}}}
					if _, _, err := matchingSelection(candidate, old, priced); err == nil {
						copy := *priced.MilesCash
						fare.MilesCash = &copy
						fare.Estimated = priced.Estimated
					}
				}
			}
		}
	}
}
