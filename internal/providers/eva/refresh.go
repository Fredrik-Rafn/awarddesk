package eva

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/session"
)

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

func canRetryAvailabilityRead(err error) bool {
	var failure *award.Error
	if !errors.As(err, &failure) {
		return false
	}
	return failure.Code == "connection" || failure.Code == "response_read" || failure.Code == "search_protocol" ||
		(failure.Code == "provider_http" && failure.HTTPStatus >= 500 && failure.HTTPStatus <= 599)
}
