package eva

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"

	"awarddesk/internal/award"
)

func recoveryAvailability(secondPrice int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"currencyBean":{"code":"TWD"},"proposedBounds":[{"proposedFlightsGroup":[{"proposedBoundId":1,"segments":[{"beginLocation":{"locationCode":"TPE"},"endLocation":{"locationCode":"HKG"},"beginDate":"2026-11-24T12:00:00","endDate":"2026-11-24T14:00:00","flightNumber":"123","airline":{"code":"BR"}}]}]}],"fareFamilyList":[{"ffCode":"basic","ffName":"Economy Basic"},{"ffCode":"up","ffName":"Economy Up"}],"recommendationList":[{"recoId":1,"ffCode":"basic","recoAmount":{"totalAmount":100},"bounds":[{"flightGroupList":[{"flightId":1}]}]},{"recoId":2,"ffCode":"up","recoAmount":{"totalAmount":%d},"bounds":[{"flightGroupList":[{"flightId":1}]}]}]}`, secondPrice))
}

func TestPricingReplansChangedAvailabilityWithoutRepricingFinishedFare(t *testing.T) {
	q := award.Query{Provider: "eva", Origin: "TPE", Destination: "HKG", Date: "2026-11-24", Cabin: award.Economy, Adults: 1}
	r, err := parseAvailability(recoveryAvailability(200), q)
	if err != nil {
		t.Fatal(err)
	}
	var bookings, fares, refreshes, recoveries int
	selected := ""
	c := pricingTransport(func(ctx context.Context, method, target string, body io.Reader, h http.Header) (*http.Response, []byte, error) {
		b, _ := io.ReadAll(body)
		v, _ := url.ParseQuery(string(b))
		business := map[string]json.RawMessage{}
		switch {
		case strings.Contains(target, "FlexPricerAvailability"):
			refreshes++
			business["Availability"] = recoveryAvailability(220)
		case strings.Contains(target, "AjaxCall"):
			fares++
			selected = v.Get("RECOMMENDATION_ID_1")
			amount := 100
			if selected == "2" {
				amount = 220
			}
			business["Price"] = json.RawMessage(fmt.Sprintf(`{"totalAmount":{"amount":%d,"currency":{"code":"TWD"}}}`, amount))
		case strings.Contains(target, "AddElementsTravellerInformation"):
			business["TravellerList"] = json.RawMessage(`{"Travellers":[{"Type":{"code":"ADT"},"IdentityInformation":{"IDEN_TitleCode":"MR","IDEN_FirstName":"TEST","IDEN_LastName":"MEMBER","IDEN_DateOfBirth":"1990/05/09","IDEN_Nationality":"TW"}}]}`)
			business["ContactInformation"] = json.RawMessage(`{"Email":"test@example.test","PhoneMobile":"123456789","PhoneMobileCode":"886"}`)
		case strings.Contains(target, "AddDynamicCatalogSelectionToTripPlan"):
			bookings++
			business["MilesAndCashView"] = json.RawMessage(`{"maxRankAvailable":{"milesAmount":1000,"nonConvertedAmount":{"amount":50}}}`)
		case !strings.Contains(target, "AddElementsDynamicCatalog"):
			t.Fatal("unexpected pricing endpoint")
		}
		response, _ := json.Marshal(map[string]any{"data": pageData{PageTicket: "fresh", Business: business}, "sessionId": "session"})
		return &http.Response{StatusCode: 200}, response, nil
	})
	err = priceFaresWithReplans(context.Background(), c, pageData{SessionID: "session", PageTicket: "initial"}, &r, func(p award.Progress) {
		if p.Recovery != nil {
			recoveries++
		}
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Complete || bookings != 2 || fares != 2 || refreshes != 1 || recoveries != 1 || r.Itineraries[0].Fares[1].CashPrice.Amount != "220" {
		t.Fatalf("unsafe or incomplete recovery: complete=%v bookings=%d fares=%d refresh=%d recoveries=%d", r.Complete, bookings, fares, refreshes, recoveries)
	}
}

func TestChangedFareDoesNotInheritOldMilesQuote(t *testing.T) {
	q := award.Query{Origin: "TPE", Destination: "HKG", Date: "2026-11-24", Cabin: award.Economy}
	old, _ := parseAvailability(recoveryAvailability(200), q)
	for n := range old.Itineraries[0].Fares {
		old.Itineraries[0].Fares[n].MilesCash = &award.MilesCash{Miles: 12345}
	}
	fresh, _ := parseAvailability(recoveryAvailability(220), q)
	fresh.Itineraries[0].Fares[0].QuoteID = "99"
	preserveMatchingQuotes(&fresh, old)
	if fresh.Itineraries[0].Fares[0].MilesCash == nil || fresh.Itineraries[0].Fares[0].QuoteID != "99" || fresh.Itineraries[0].Fares[1].MilesCash != nil {
		t.Fatal("stale quote or identifier carried to changed fare")
	}
}

func TestMissingSelectedFareStateRefreshesBeforeCheckout(t *testing.T) {
	q := award.Query{Provider: "eva", Origin: "TPE", Destination: "HKG", Date: "2026-11-24", Cabin: award.Economy, Adults: 1}
	result, e := parseAvailability(recoveryAvailability(200), q)
	if e != nil {
		t.Fatal(e)
	}
	selections, bookings, refreshes, recoveries := 0, 0, 0, 0
	c := pricingTransport(func(ctx context.Context, method, target string, body io.Reader, h http.Header) (*http.Response, []byte, error) {
		b, _ := io.ReadAll(body)
		values, _ := url.ParseQuery(string(b))
		business := map[string]json.RawMessage{}
		switch {
		case strings.Contains(target, "FlexPricerAvailability"):
			refreshes++
			business["Availability"] = recoveryAvailability(200)
		case strings.Contains(target, "AjaxCall"):
			selections++
			if selections > 1 {
				amount := 100
				if values.Get("RECOMMENDATION_ID_1") == "2" {
					amount = 200
				}
				business["Price"] = json.RawMessage(fmt.Sprintf(`{"totalAmount":{"amount":%d,"currency":{"code":"TWD"}}}`, amount))
			}
		case strings.Contains(target, "AddElementsTravellerInformation"):
			if selections < 2 {
				t.Fatal("checkout started before fare selection was validated")
			}
			business["TravellerList"] = json.RawMessage(`{"Travellers":[{"Type":{"code":"ADT"},"IdentityInformation":{"IDEN_TitleCode":"MR","IDEN_FirstName":"TEST","IDEN_LastName":"MEMBER","IDEN_DateOfBirth":"1990/05/09","IDEN_Nationality":"TW"}}]}`)
			business["ContactInformation"] = json.RawMessage(`{"Email":"test@example.test","PhoneMobile":"123456789","PhoneMobileCode":"886"}`)
		case strings.Contains(target, "AddDynamicCatalogSelectionToTripPlan"):
			bookings++
			business["MilesAndCashView"] = json.RawMessage(`{"maxRankAvailable":{"milesAmount":1000,"nonConvertedAmount":{"amount":50}}}`)
		case !strings.Contains(target, "AddElementsDynamicCatalog"):
			t.Fatal("unexpected endpoint")
		}
		response, _ := json.Marshal(map[string]any{"data": pageData{PageTicket: "fresh", Business: business}, "sessionId": "session"})
		return &http.Response{StatusCode: 200}, response, nil
	})
	e = priceFaresWithReplans(context.Background(), c, pageData{SessionID: "session", PageTicket: "initial"}, &result, func(p award.Progress) {
		if p.Recovery != nil {
			recoveries++
		}
	}, 2)
	if e != nil || !result.Complete || selections != 3 || bookings != 2 || refreshes != 2 || recoveries != 1 {
		t.Fatalf("recovery error=%v complete=%v selections=%d bookings=%d refreshes=%d recoveries=%d", e, result.Complete, selections, bookings, refreshes, recoveries)
	}
}
