package eva

import (
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"awarddesk/internal/award"
	http "github.com/bogdanfinn/fhttp"
)

func availabilityTaxFixture(t *testing.T) (award.Result, award.Result, pageData, map[string]any) {
	t.Helper()
	result, cash, page, _, _ := awardTaxFlowFixture(t)
	cash.Itineraries[0].ID = "42"
	cash.Itineraries[0].Fares[0].QuoteID = "84"
	cash.Itineraries[0].Fares[0].CashPrice.Amount = "10358"
	amount := func() map[string]any {
		return map[string]any{"totalAmount": 5179, "amountWithoutTax": 2132, "amountWithoutTaxAndFee": 2132, "tax": 3047, "serviceFee": 0, "hasDynamicDiscount": false}
	}
	a := map[string]any{
		"currencyBean":       map[string]any{"code": "TWD"},
		"fareFamilyList":     []any{map[string]any{"ffCode": "ECO", "ffName": "Economy Basic"}},
		"recommendationList": []any{map[string]any{"recoId": 84, "ffCode": "ECO", "recoAmount": amount(), "bounds": []any{map[string]any{"boundAmount": amount(), "travellerPrices": map[string]any{"ADT": 5179}, "flightGroupList": []any{map[string]any{"flightId": 42}}}}}},
	}
	page.Business = map[string]json.RawMessage{"Availability": taxJSON(t, a), "Price": json.RawMessage(`{"isMilesMode":false}`)}
	return result, cash, page, a
}

func taxJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAvailabilityTaxEstimateReconcilesPerAdultAndPartyAmount(t *testing.T) {
	result, cash, page, _ := availabilityTaxFixture(t)
	flight, fare := cash.Itineraries[0], cash.Itineraries[0].Fares[0]
	estimate, ok := cashAvailabilityTaxEstimate(page, flight, fare, result.Query.Adults, time.Now())
	if !ok || estimate.Source != "eva_cash_availability" || !equalCashAmounts(estimate.Total.Amount, "6094") || estimate.Total.Currency != "TWD" || len(estimate.Components) != 0 {
		t.Fatalf("invalid party estimate: %+v ok=%v", estimate, ok)
	}
}

func TestAvailabilityTaxEstimateRejectsUnverifiedUnitsAndReferences(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any, map[string]any, map[string]any){
		"missing tax": func(_ map[string]any, r, _ map[string]any) { delete(r["recoAmount"].(map[string]any), "tax") },
		"wrong total": func(_ map[string]any, r, _ map[string]any) { r["recoAmount"].(map[string]any)["totalAmount"] = 6000 },
		"dynamic discount": func(_ map[string]any, r, _ map[string]any) {
			r["recoAmount"].(map[string]any)["hasDynamicDiscount"] = true
		},
		"extra service fee":       func(_ map[string]any, r, _ map[string]any) { r["recoAmount"].(map[string]any)["serviceFee"] = 100 },
		"negative tax":            func(_ map[string]any, r, _ map[string]any) { r["recoAmount"].(map[string]any)["tax"] = -1 },
		"bound differs":           func(_, _ map[string]any, b map[string]any) { b["boundAmount"].(map[string]any)["tax"] = 3000 },
		"traveller price differs": func(_, _ map[string]any, b map[string]any) { b["travellerPrices"].(map[string]any)["ADT"] = 10358 },
		"extra traveller type":    func(_, _ map[string]any, b map[string]any) { b["travellerPrices"].(map[string]any)["CHD"] = 1000 },
		"unknown flight": func(_, _ map[string]any, b map[string]any) {
			b["flightGroupList"] = []any{map[string]any{"flightId": 43}}
		},
		"duplicate flight": func(_, _ map[string]any, b map[string]any) {
			b["flightGroupList"] = []any{map[string]any{"flightId": 42}, map[string]any{"flightId": 42}}
		},
		"duplicate recommendation": func(a, r, _ map[string]any) { a["recommendationList"] = []any{r, r} },
		"wrong currency":           func(a, _, _ map[string]any) { a["currencyBean"].(map[string]any)["code"] = "USD" },
		"wrong family":             func(_ map[string]any, r, _ map[string]any) { r["ffCode"] = "BUS" },
	} {
		t.Run(name, func(t *testing.T) {
			result, cash, page, a := availabilityTaxFixture(t)
			r := a["recommendationList"].([]any)[0].(map[string]any)
			b := r["bounds"].([]any)[0].(map[string]any)
			mutate(a, r, b)
			page.Business["Availability"] = taxJSON(t, a)
			if _, ok := cashAvailabilityTaxEstimate(page, cash.Itineraries[0], cash.Itineraries[0].Fares[0], result.Query.Adults, time.Now()); ok {
				t.Fatal("unverified availability tax accepted")
			}
		})
	}
	for _, mode := range []string{`{}`, `{"isMilesMode":true}`} {
		result, cash, page, _ := availabilityTaxFixture(t)
		page.Business["Price"] = json.RawMessage(mode)
		if _, ok := cashAvailabilityTaxEstimate(page, cash.Itineraries[0], cash.Itineraries[0].Fares[0], result.Query.Adults, time.Now()); ok {
			t.Fatal("cash mode was not verified")
		}
	}
}

func TestAvailabilityTaxesQuoteAllMatchingFlightsWithoutSelectionRequests(t *testing.T) {
	result, cash, page, a := availabilityTaxFixture(t)
	second := award.CloneResult(result).Itineraries[0]
	second.ID, second.Segments[0].FlightNumber = "other-award", "BR2"
	result.Itineraries = append(result.Itineraries, second)
	otherCash := award.CloneResult(cash).Itineraries[0]
	otherCash.ID, otherCash.Segments[0].FlightNumber = "43", "BR2"
	cash.Itineraries = append(cash.Itineraries, otherCash)
	bound := a["recommendationList"].([]any)[0].(map[string]any)["bounds"].([]any)[0].(map[string]any)
	bound["flightGroupList"] = []any{map[string]any{"flightId": 42}, map[string]any{"flightId": 43}}
	page.Business["Availability"] = taxJSON(t, a)
	c := pricingTransport(func(context.Context, string, string, io.Reader, http.Header) (*http.Response, []byte, error) {
		t.Fatal("bulk tax estimates submitted a selection or refreshed availability")
		return nil, nil, nil
	})
	if err := collectAwardTaxEstimates(context.Background(), c, page, cash, &result, nil); err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Itineraries {
		f := item.Fares[0]
		if f.Points != 35000 || f.Taxes.Amount != "" || f.TaxEstimate == nil || !equalCashAmounts(f.TaxEstimate.Total.Amount, "6094") {
			t.Fatal("award quote or estimate incorrect")
		}
	}
	result.Itineraries[0].Fares[0].TaxEstimate.Total.Amount = "0"
	if !equalCashAmounts(result.Itineraries[1].Fares[0].TaxEstimate.Total.Amount, "6094") {
		t.Fatal("flights share a mutable tax estimate")
	}
}

func TestFareFallbackRefreshesOnceThenUsesBulkForRemainingFlights(t *testing.T) {
	result, cash, page, price, view := awardTaxFlowFixture(t)
	_, _, _, availability := availabilityTaxFixture(t)
	var proposed []any
	var groups []any
	for n := 2; n <= 3; n++ {
		number := strconv.Itoa(n)
		item := award.CloneResult(result).Itineraries[0]
		item.ID, item.Segments[0].FlightNumber = "award-"+number, "BR"+number
		result.Itineraries = append(result.Itineraries, item)
		id := 41 + n
		segment := item.Segments[0]
		proposed = append(proposed, map[string]any{"proposedBoundId": id, "segments": []any{map[string]any{
			"beginLocation": map[string]any{"locationCode": "TPE"}, "endLocation": map[string]any{"locationCode": "HKG"},
			"beginDate": segment.DepartureLocal, "endDate": segment.ArrivalLocal, "flightNumber": number, "airline": map[string]any{"code": "BR", "name": "EVA Air"},
		}}})
		groups = append(groups, map[string]any{"flightId": id, "numberOfSeatsLeft": 2, "rbd": "V"})
	}
	availability["proposedBounds"] = []any{map[string]any{"proposedFlightsGroup": proposed}}
	availability["recommendationList"].([]any)[0].(map[string]any)["bounds"].([]any)[0].(map[string]any)["flightGroupList"] = groups
	calls := 0
	c := pricingTransport(func(_ context.Context, method, target string, body io.Reader, _ http.Header) (*http.Response, []byte, error) {
		calls++
		_, u := assertAwardTaxFlowRequest(t, method, target, body)
		if calls == 1 && u.Query().Get("UID") == "FARE" {
			return &http.Response{StatusCode: 200}, awardTaxFlowResponse("ticket2", "nav2", map[string]any{"Price": price, "ListItineraryView": view}), nil
		}
		if calls == 2 && strings.Contains(u.Path, "FlexPricerAvailability.action") {
			return &http.Response{StatusCode: 200}, awardTaxFlowResponse("ticket3", "nav3", map[string]any{"Availability": availability, "Price": map[string]any{"isMilesMode": false}}), nil
		}
		t.Fatal("bulk candidates repeated FARE or availability refresh")
		return nil, nil, nil
	})
	if err := collectAwardTaxEstimates(context.Background(), c, page, cash, &result, nil); err != nil || calls != 2 {
		t.Fatalf("mixed tax flow failed: calls=%d error=%v", calls, err)
	}
	for n, item := range result.Itineraries {
		estimate := item.Fares[0].TaxEstimate
		if estimate == nil || !equalCashAmounts(estimate.Total.Amount, "6094") || (n == 0 && estimate.Source != "eva_cash_fare") || (n > 0 && estimate.Source != "eva_cash_availability") {
			t.Fatal("mixed tax estimates were missing or misattributed")
		}
	}
}
