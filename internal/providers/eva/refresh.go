package eva

import (
	"awarddesk/internal/award"
	"context"
	"encoding/json"
	"strconv"
	"strings"
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
	v.Set("TRAVELLER_TYPE_1", "ADT")
	v.Set("HAS_INFANT_1", "FALSE")
	var cfg struct{ DateRangeQualifier string }
	if json.Unmarshal(initial.Config["FPOW"], &cfg) == nil && cfg.DateRangeQualifier != "" {
		v.Set("DATE_RANGE_QUALIFIER_1", cfg.DateRangeQualifier)
	}
	next, err := quoteRequest(ctx, c, current.SessionID, "refresh availability", v)
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
