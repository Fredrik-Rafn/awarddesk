package eva

import (
	"awarddesk/internal/award"
	"awarddesk/internal/session"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

const ajaxURL = "https://book.evaair.com/plnext/EVAOnlineDX/AjaxCall.action"

type pricingClient interface {
	Do(context.Context, string, string, io.Reader, http.Header) (*http.Response, []byte, error)
}

type timedPricingClient interface {
	DoWithTimeout(context.Context, string, string, io.Reader, http.Header, time.Duration) (*http.Response, []byte, error)
}

func ajaxContext(page pageData) url.Values {
	values := url.Values{"IS_MILES_MODE": {"false"}, "PAGE_TICKET": {page.PageTicket}}
	for _, key := range []string{"LANGUAGE", "COUNTRY_SITE", "SITE", "BOOKING_FLOW", "TRIP_FLOW", "EXTERNAL_ID", "OFFICE_ID", "TRIP_TYPE", "PRICING_TYPE"} {
		values.Set(key, page.Basefacts["request."+key])
	}
	return values
}

// Endpoints are fixed to the captured price-preparation flow. Payment and
// ticketing endpoints are never accepted.
func quoteRequest(ctx context.Context, c pricingClient, sessionID, action string, values url.Values) (pageData, error) {
	started := time.Now()
	defer func() {
		slog.Info("EVA pricing request", "action", action, "elapsed", time.Since(started).Round(time.Millisecond))
	}()
	if !regexp.MustCompile(`^[A-Za-z0-9_!.~-]{1,256}$`).MatchString(sessionID) {
		return pageData{}, award.Failure("pricing_protocol", "EVA booking session identifier missing or invalid")
	}
	endpoint := ajaxURL + ";jsessionid=" + sessionID
	var data []byte
	var err error
	request := c.Do
	if timed, ok := c.(timedPricingClient); ok {
		request = func(ctx context.Context, m, u string, b io.Reader, h http.Header) (*http.Response, []byte, error) {
			return timed.DoWithTimeout(ctx, m, u, b, h, 90*time.Second)
		}
	}
	headers := http.Header{"sec-fetch-site": {"same-origin"}, "referer": {overrideURL}, "origin": {"https://book.evaair.com"}, "sec-fetch-mode": {"cors"}, "sec-fetch-dest": {"empty"}, "accept": {"*/*"}, "x-requested-with": {"XMLHttpRequest"}}
	switch action {
	case "fare":
		headers["content-type"] = []string{"application/x-www-form-urlencoded"}
		_, data, err = request(ctx, "POST", endpoint+"?UID=FARE&UI_ACTION=ajax", strings.NewReader(values.Encode()), headers)
	case "travellers", "catalog", "checkout price":
		path := map[string]string{"travellers": "AddElementsTravellerInformation", "catalog": "AddElementsDynamicCatalog", "checkout price": "AddDynamicCatalogSelectionToTripPlan"}[action]
		if action == "checkout price" && (values.Get("ACTION") != "BOOK" || values.Get("TRANSACTION_ID") != "RequestAdditionalInformation" || values.Get("CONFIRM_SERVICE_SELECTION") != "TRUE") {
			return pageData{}, award.Failure("unsafe_request", "Unsupported EVA price-preparation operation")
		}
		headers["content-type"] = []string{"application/x-www-form-urlencoded;"}
		_, data, err = request(ctx, "POST", "https://book.evaair.com/plnext/EVAOnlineDX/"+path+".action;jsessionid="+sessionID, strings.NewReader(values.Encode()), headers)
	case "refresh availability":
		headers["content-type"] = []string{"application/x-www-form-urlencoded;"}
		_, data, err = request(ctx, "POST", "https://book.evaair.com/plnext/EVAOnlineDX/FlexPricerAvailability.action;jsessionid="+sessionID, strings.NewReader(values.Encode()), headers)
	default:
		return pageData{}, award.Failure("unsafe_request", "Unsupported EVA pricing operation")
	}
	if err != nil {
		return pageData{}, err
	}
	return decodePage(data)
}

func parseMilesCash(raw json.RawMessage, currency string) (*award.MilesCash, error) {
	type rank struct {
		MilesAmount        json.Number
		NonConvertedAmount struct{ Amount json.Number }
	}
	var m struct {
		IsAvailable      bool
		MaxRankAvailable *rank
		ListRanks        []rank
	}
	if json.Unmarshal(raw, &m) != nil || m.MaxRankAvailable == nil {
		return nil, award.Failure("pricing_unavailable", "EVA did not return a Miles & Cash calculation")
	}
	miles, err := m.MaxRankAvailable.MilesAmount.Float64()
	if err != nil || miles < 0 || miles > 1e9 || miles != float64(int(miles)) {
		return nil, award.Failure("pricing_protocol", "EVA returned an invalid mileage amount")
	}
	cash, err := m.MaxRankAvailable.NonConvertedAmount.Amount.Float64()
	if err != nil || cash < 0 || cash > 1e9 {
		return nil, award.Failure("pricing_protocol", "EVA returned an invalid cash remainder")
	}
	// maxRankAvailable describes a theoretical maximum, while listRanks
	// contains the account's usable slider positions. Availability alone does
	// not mean this particular maximum is selectable.
	selectable := false
	if m.IsAvailable {
		for _, r := range m.ListRanks {
			rankMiles, milesErr := r.MilesAmount.Float64()
			rankCash, cashErr := r.NonConvertedAmount.Amount.Float64()
			if milesErr == nil && cashErr == nil && rankMiles == miles && rankCash == cash {
				selectable = true
				break
			}
		}
	}
	return &award.MilesCash{Miles: int(miles), RemainingCash: award.Money{Amount: m.MaxRankAvailable.NonConvertedAmount.Amount.String(), Currency: currency}, Selectable: selectable}, nil
}

func priceFares(ctx context.Context, c *session.Client, page pageData, result *award.Result, report award.Reporter) error {
	return priceFaresWithReplans(ctx, c, page, result, report, 2)
}

func priceFaresWithReplans(ctx context.Context, c pricingClient, page pageData, result *award.Result, report award.Reporter, replans int) error {
	initialPage := page
	seen := map[string]bool{}
	total := 0
	for _, i := range result.Itineraries {
		for _, f := range i.Fares {
			if f.MilesCash != nil {
				continue
			}
			if f.MilesCashEligible != nil && !*f.MilesCashEligible {
				continue
			}
			if !seen[f.QuoteID] {
				seen[f.QuoteID] = true
				total++
			}
		}
	}
	if total > 30 {
		result.Warnings = append(result.Warnings, "EVA returned more fare groups than the bounded pricing limit.")
		return nil
	}
	seen = map[string]bool{}
	done := 0
	for _, i := range result.Itineraries {
		for _, f := range i.Fares {
			if f.MilesCash != nil {
				continue
			}
			if f.MilesCashEligible != nil && !*f.MilesCashEligible {
				continue
			}
			if seen[f.QuoteID] {
				continue
			}
			seen[f.QuoteID] = true
			if _, err := strconv.Atoi(f.QuoteID); err != nil {
				return award.Failure("pricing_protocol", "EVA fare identifier missing")
			}
			if report != nil {
				report(award.Progress{Stage: "miles pricing", Message: "Calculating Miles & Cash for each distinct fare", Completed: done, Total: total})
			}
			values := ajaxContext(page)
			flightID, recommendationID := i.ID, f.QuoteID
			if done > 0 {
				var fresh award.Result
				var refreshErr error
				page, fresh, refreshErr = refreshAvailability(ctx, c, page, initialPage, result.Query)
				if refreshErr != nil {
					return refreshErr
				}
				flightID, recommendationID, refreshErr = matchingSelection(fresh, i, f)
				if refreshErr != nil {
					var changed *award.Error
					if replans > 0 && errors.As(refreshErr, &changed) && changed.Code == "price_changed" {
						preserveMatchingQuotes(&fresh, *result)
						*result = fresh
						if report != nil {
							report(award.Progress{Stage: "miles pricing", Message: "EVA changed availability; continuing with refreshed fares and preserving unchanged quotes", Recovery: recoveryDetail("miles pricing", refreshErr, 3-replans)})
						}
						return priceFaresWithReplans(ctx, c, page, result, report, replans-1)
					}
					return refreshErr
				}
				values = ajaxContext(page)
			}
			values.Set("RECOMMENDATION_ID_1", recommendationID)
			values.Set("FLIGHT_ID_1", flightID)
			selected, err := quoteRequest(ctx, c, page.SessionID, "fare", values)
			if err != nil {
				return err
			}
			if selected.PageTicket == "" {
				return award.Failure("pricing_protocol", "EVA omitted updated pricing state")
			}
			page.PageTicket = selected.PageTicket
			if selected.SessionID != "" {
				page.SessionID = selected.SessionID
			}
			var price struct {
				IsMilesMode bool
				TotalAmount struct {
					Amount   json.Number
					Currency namedCode
				}
			}
			if json.Unmarshal(selected.Business["Price"], &price) != nil || price.IsMilesMode || price.TotalAmount.Currency.Code != f.CashPrice.Currency {
				return award.Failure("pricing_protocol", "EVA pricing context changed")
			}
			want, e1 := strconv.ParseFloat(f.CashPrice.Amount, 64)
			got, e2 := price.TotalAmount.Amount.Float64()
			if e1 != nil || e2 != nil || want != got {
				return award.Failure("price_changed", "EVA changed the selected fare; run a fresh search")
			}
			calculated, err := checkoutQuote(ctx, c, page, result.Query)
			if err != nil {
				return err
			}
			if calculated.PageTicket != "" {
				page.PageTicket = calculated.PageTicket
			}
			if calculated.SessionID != "" {
				page.SessionID = calculated.SessionID
			}
			quote, err := parseMilesCash(calculated.Business["MilesAndCashView"], f.CashPrice.Currency)
			if err != nil {
				result.Warnings = append(result.Warnings, err.Error())
				return nil
			}
			for x := range result.Itineraries {
				for y := range result.Itineraries[x].Fares {
					fare := &result.Itineraries[x].Fares[y]
					if fare.QuoteID == f.QuoteID && (fare.MilesCashEligible == nil || *fare.MilesCashEligible) {
						fare.MilesCash = quote
						fare.Estimated = true
					}
				}
			}
			done++
		}
	}
	result.Complete = done == total
	if result.Complete {
		result.Warnings = []string{"Miles & Cash amounts are maximum-offset estimates for aggregation, not award tickets. No payment or ticketing request was submitted."}
	}
	return nil
}
