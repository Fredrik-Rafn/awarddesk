package eva

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"awarddesk/internal/award"
	http "github.com/bogdanfinn/fhttp"
)

func awardTaxFlowFixture(t *testing.T) (award.Result, award.Result, pageData, map[string]any, map[string]any) {
	t.Helper()
	q, target, cash := cashTaxMatchFixture()
	target.Fares = []award.Fare{{Name: "Award", Cabin: q.Cabin, Points: 35000, PricingType: "award", AvailableFor: q.Adults}}
	result := award.Result{Query: q, FetchedAt: time.Now().UTC(), Complete: true, Itineraries: []award.Itinerary{target}, Warnings: []string{}}
	_, _, view := cashTaxSelectedViewFixture()
	price := cashTaxFixture(t)
	price["totalTaxes"].(map[string]any)["amount"] = 6094
	price["totalAmount"].(map[string]any)["amount"] = 10358
	cashTaxGroup(price)["traveller"].(map[string]any)["travellersNumber"] = q.Adults
	page := pageData{SessionID: "nav1", PageTicket: "ticket1", Basefacts: map[string]string{"request.LANGUAGE": "GB"}}
	return result, cash, page, price, view
}

func awardTaxFlowResponse(ticket, session string, business map[string]any) []byte {
	raw, _ := json.Marshal(map[string]any{"data": map[string]any{"pageTicket": ticket, "business": business}, "sessionId": session})
	return raw
}

func assertAwardTaxFlowRequest(t *testing.T, method, target string, body io.Reader) (url.Values, *url.URL) {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil || method != "POST" || u.Host != "book.evaair.com" {
		t.Fatal("tax lookup left expected quotation host/method")
	}
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	v, err := url.ParseQuery(string(data))
	if err != nil {
		t.Fatal(err)
	}
	for key := range v {
		if key == "ACTION" || key == "TRANSACTION_ID" || key == "CONFIRM_SERVICE_SELECTION" || strings.HasPrefix(key, "FIRST_NAME") || strings.HasPrefix(key, "LAST_NAME") || strings.HasPrefix(key, "DATE_OF_BIRTH") || strings.HasPrefix(key, "CONTACT_POINT") {
			t.Fatalf("tax lookup submitted traveller or checkout field %s", key)
		}
	}
	if v.Get("IS_MILES_MODE") != "false" {
		t.Fatal("tax lookup did not explicitly use cash pricing")
	}
	if !strings.Contains(u.Path, "/AjaxCall.action;") && !strings.Contains(u.Path, "/FlexPricerAvailability.action;") {
		t.Fatal("tax lookup reached traveller/catalog/booking endpoint")
	}
	return v, u
}

func TestAwardTaxFlowOnlyFAREAttachesPartyEstimateAndPreservesAward(t *testing.T) {
	result, cash, page, price, view := awardTaxFlowFixture(t)
	result.Itineraries[0].Fares = append(result.Itineraries[0].Fares,
		award.Fare{PricingType: "award", Cabin: result.Query.Cabin, Points: 35000, Taxes: award.Money{Amount: "33", Currency: "TWD"}},
		award.Fare{PricingType: "miles_cash", Points: 123})
	before := award.CloneResult(result)
	calls, previews := 0, 0
	c := pricingTransport(func(ctx context.Context, method, target string, body io.Reader, _ http.Header) (*http.Response, []byte, error) {
		calls++
		v, u := assertAwardTaxFlowRequest(t, method, target, body)
		if calls != 1 || u.Path != "/plnext/EVAOnlineDX/AjaxCall.action;jsessionid=nav1" || u.Query().Get("UID") != "FARE" || v.Get("PAGE_TICKET") != "ticket1" || v.Get("FLIGHT_ID_1") != cash.Itineraries[0].ID || v.Get("RECOMMENDATION_ID_1") != cash.Itineraries[0].Fares[0].QuoteID {
			t.Fatal("tax lookup submitted stale selection state or repeated FARE")
		}
		return &http.Response{StatusCode: 200}, awardTaxFlowResponse("ticket2", "nav2", map[string]any{"Price": price, "ListItineraryView": view}), nil
	})
	err := collectAwardTaxEstimates(context.Background(), c, page, cash, &result, func(p award.Progress) {
		previews++
		if p.Stage != "award tax estimates" || p.Preview != &result {
			t.Fatal("tax progress replaced award data with cash availability")
		}
	})
	if err != nil || calls != 1 || previews != 1 {
		t.Fatalf("tax enrichment failed: calls=%d previews=%d err=%v", calls, previews, err)
	}
	estimate := result.Itineraries[0].Fares[0].TaxEstimate
	if estimate == nil || estimate.Total.Amount != "6094" || estimate.Total.Currency != "TWD" || estimate.Source != "eva_cash_fare" || len(estimate.Components) != 3 || estimate.QuotedAt.IsZero() {
		t.Fatalf("missing/wrong party estimate: %+v", estimate)
	}
	result.Itineraries[0].Fares[0].TaxEstimate = nil
	if !reflect.DeepEqual(result, before) {
		t.Fatal("tax estimate changed award mileage, verified taxes, completeness or other fare data")
	}
}

func TestAwardTaxFlowMalformedOrMismatchedEvidenceRemainsUnquoted(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any, map[string]any){
		"missing breakdown":           func(price, _ map[string]any) { delete(price, "listTravellerPrice") },
		"unknown charge without code": func(price, _ map[string]any) { delete(cashTaxSections(price)[0].([]any)[1].(map[string]any), "code") },
		"wrong party": func(price, _ map[string]any) {
			cashTaxGroup(price)["traveller"].(map[string]any)["travellersNumber"] = 1
		},
		"selected flight mismatch": func(_, view map[string]any) {
			view["listItineraryElem"].([]any)[0].(map[string]any)["listSegment"].([]any)[0].(map[string]any)["flightNumber"] = "2"
		},
		"missing selected itinerary": func(_, view map[string]any) { delete(view, "listItineraryElem") },
	} {
		t.Run(name, func(t *testing.T) {
			result, cash, page, price, view := awardTaxFlowFixture(t)
			before := award.CloneResult(result)
			mutate(price, view)
			calls := 0
			c := pricingTransport(func(ctx context.Context, method, target string, body io.Reader, _ http.Header) (*http.Response, []byte, error) {
				calls++
				_, u := assertAwardTaxFlowRequest(t, method, target, body)
				if calls != 1 || u.Query().Get("UID") != "FARE" {
					t.Fatal("invalid tax data triggered another pricing action")
				}
				return &http.Response{StatusCode: 200}, awardTaxFlowResponse("ticket2", "nav2", map[string]any{"Price": price, "ListItineraryView": view}), nil
			})
			if err := collectAwardTaxEstimates(context.Background(), c, page, cash, &result, nil); err != nil || calls != 1 || !reflect.DeepEqual(result, before) {
				t.Fatalf("unverified taxes attached or award data lost: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestAwardTaxFlowCanceledContextMakesNoRequest(t *testing.T) {
	result, cash, page, _, _ := awardTaxFlowFixture(t)
	before := award.CloneResult(result)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := pricingTransport(func(context.Context, string, string, io.Reader, http.Header) (*http.Response, []byte, error) {
		t.Fatal("cancelled tax lookup issued HTTP")
		return nil, nil, nil
	})
	if err := collectAwardTaxEstimates(ctx, c, page, cash, &result, nil); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, before) {
		t.Fatalf("cancellation ignored or award changed: %v", err)
	}
}

func TestAwardTaxFlowLostFAREResponseIsNotReplayed(t *testing.T) {
	result, cash, page, _, _ := awardTaxFlowFixture(t)
	before := award.CloneResult(result)
	calls := 0
	want := award.Failure("connection", "response interrupted")
	c := pricingTransport(func(ctx context.Context, method, target string, body io.Reader, _ http.Header) (*http.Response, []byte, error) {
		calls++
		assertAwardTaxFlowRequest(t, method, target, body)
		return nil, nil, want
	})
	if err := collectAwardTaxEstimates(context.Background(), c, page, cash, &result, nil); err != want || calls != 1 || !reflect.DeepEqual(result, before) {
		t.Fatalf("uncertain FARE replayed or award changed: calls=%d err=%v", calls, err)
	}
}

func TestAwardTaxFlowRefreshesAvailabilityAndRotatesStateForEverySelection(t *testing.T) {
	for _, malformedFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "both quoted", true: "malformed first still quotes second"}[malformedFirst], func(t *testing.T) {
			result, cash, page, price, view := awardTaxFlowFixture(t)
			second := award.CloneResult(result).Itineraries[0]
			second.ID, second.Segments[0].FlightNumber, second.Segments[0].DepartureLocal, second.Segments[0].ArrivalLocal = "second-award", "BR2", result.Query.Date+"T11:00:00", result.Query.Date+"T13:00:00"
			result.Itineraries = append(result.Itineraries, second)
			staleSecond := cash.Itineraries[0]
			staleSecond.ID, staleSecond.Segments = "stale-second-bound", append([]award.Segment(nil), second.Segments...)
			staleSecond.Fares = append([]award.Fare(nil), staleSecond.Fares...)
			staleSecond.Fares[0].QuoteID = "stale-second-reco"
			cash.Itineraries = append(cash.Itineraries, staleSecond)
			calls := 0
			c := pricingTransport(func(ctx context.Context, method, target string, body io.Reader, _ http.Header) (*http.Response, []byte, error) {
				calls++
				v, u := assertAwardTaxFlowRequest(t, method, target, body)
				switch calls {
				case 1:
					if u.Query().Get("UID") != "FARE" || v.Get("PAGE_TICKET") != "ticket1" || v.Get("FLIGHT_ID_1") != "fresh-bound" {
						t.Fatal("first FARE used unexpected state")
					}
					if malformedFirst {
						return &http.Response{StatusCode: 200}, awardTaxFlowResponse("ticket2", "nav2", map[string]any{"Price": map[string]any{}, "ListItineraryView": view}), nil
					}
					return &http.Response{StatusCode: 200}, awardTaxFlowResponse("ticket2", "nav2", map[string]any{"Price": price, "ListItineraryView": view}), nil
				case 2:
					if u.Path != "/plnext/EVAOnlineDX/FlexPricerAvailability.action;jsessionid=nav2" || v.Get("PAGE_TICKET") != "ticket2" || v.Get("B_DATE_1") != "202611270000" || v.Get("TRAVELLER_TYPE_2") != "ADT" {
						t.Fatal("second flight did not refresh current party availability with rotated state")
					}
					availability := map[string]any{
						"currencyBean": map[string]any{"code": "TWD"}, "fareFamilyList": []any{map[string]any{"ffCode": "ECO", "ffName": "Economy Basic"}},
						"proposedBounds":     []any{map[string]any{"proposedFlightsGroup": []any{map[string]any{"proposedBoundId": 42, "segments": []any{map[string]any{"beginLocation": map[string]any{"locationCode": "TPE"}, "endLocation": map[string]any{"locationCode": "HKG"}, "beginDate": second.Segments[0].DepartureLocal, "endDate": second.Segments[0].ArrivalLocal, "airline": map[string]any{"code": "BR", "name": "EVA Air"}, "flightNumber": "2"}}}}}},
						"recommendationList": []any{map[string]any{"recoId": 84, "ffCode": "ECO", "recoAmount": map[string]any{"totalAmount": 100}, "bounds": []any{map[string]any{"flightGroupList": []any{map[string]any{"flightId": 42, "numberOfSeatsLeft": 2, "rbd": "A"}}}}}},
					}
					return &http.Response{StatusCode: 200}, awardTaxFlowResponse("ticket3", "nav3", map[string]any{"Availability": availability}), nil
				case 3:
					if u.Path != "/plnext/EVAOnlineDX/AjaxCall.action;jsessionid=nav3" || u.Query().Get("UID") != "FARE" || v.Get("PAGE_TICKET") != "ticket3" || v.Get("FLIGHT_ID_1") != "42" || v.Get("RECOMMENDATION_ID_1") != "84" {
						t.Fatal("second FARE reused old session/ticket/flight/recommendation IDs")
					}
					_, _, secondView := cashTaxSelectedViewFixture()
					segment := secondView["listItineraryElem"].([]any)[0].(map[string]any)["listSegment"].([]any)[0].(map[string]any)
					segment["flightNumber"], segment["beginDate"], segment["endDate"] = "2", second.Segments[0].DepartureLocal, second.Segments[0].ArrivalLocal
					return &http.Response{StatusCode: 200}, awardTaxFlowResponse("ticket4", "nav4", map[string]any{"Price": price, "ListItineraryView": secondView}), nil
				default:
					t.Fatal("tax enrichment issued an extra request")
					return nil, nil, nil
				}
			})
			if err := collectAwardTaxEstimates(context.Background(), c, page, cash, &result, nil); err != nil || calls != 3 {
				t.Fatalf("multi-flight tax lookup failed: calls=%d err=%v", calls, err)
			}
			if (result.Itineraries[0].Fares[0].TaxEstimate == nil) != malformedFirst || result.Itineraries[1].Fares[0].TaxEstimate == nil {
				t.Fatal("estimates missing, or malformed first estimate attached")
			}
		})
	}
}

func TestAwardTaxFlowFreshContextSkipsCompletedEstimateAndQuotesMissingFlightIndependently(t *testing.T) {
	result, cash, _, price, view := awardTaxFlowFixture(t)
	firstEstimate := &award.TaxEstimate{Total: award.Money{Amount: "6094", Currency: "TWD"}, Source: "eva_cash_fare", QuotedAt: time.Now().UTC()}
	result.Itineraries[0].Fares[0].TaxEstimate = firstEstimate
	firstBefore := award.CloneResult(result).Itineraries[0]
	second := award.CloneResult(result).Itineraries[0]
	second.ID = "missing-award-flight"
	second.Segments[0].FlightNumber, second.Segments[0].DepartureLocal, second.Segments[0].ArrivalLocal = "BR2", result.Query.Date+"T11:00:00", result.Query.Date+"T13:00:00"
	second.Fares[0].TaxEstimate = nil
	result.Itineraries = append(result.Itineraries, second)
	newCash := cash.Itineraries[0]
	newCash.ID, newCash.Segments, newCash.Fares = "new-bound-2", append([]award.Segment(nil), second.Segments...), append([]award.Fare(nil), newCash.Fares...)
	newCash.Fares[0].QuoteID = "new-reco-2"
	cash.Itineraries = append(cash.Itineraries, newCash)
	segment := view["listItineraryElem"].([]any)[0].(map[string]any)["listSegment"].([]any)[0].(map[string]any)
	segment["flightNumber"], segment["beginDate"], segment["endDate"] = "2", second.Segments[0].DepartureLocal, second.Segments[0].ArrivalLocal

	price["totalTaxes"].(map[string]any)["amount"] = json.Number("1.2")
	price["totalTaxesPerPax"].(map[string]any)["amount"] = json.Number("0.6")
	sections := cashTaxSections(price)
	sections[0].([]any)[1].(map[string]any)["value"] = json.Number("0.1")
	sections[0].([]any)[2].(map[string]any)["value"] = json.Number("0.2")
	sections[1].([]any)[0].(map[string]any)["value"] = json.Number("0.3")
	calls := 0
	c := pricingTransport(func(ctx context.Context, method, target string, body io.Reader, _ http.Header) (*http.Response, []byte, error) {
		calls++
		v, u := assertAwardTaxFlowRequest(t, method, target, body)
		if calls != 1 || u.Path != "/plnext/EVAOnlineDX/AjaxCall.action;jsessionid=new-context" || u.Query().Get("UID") != "FARE" || v.Get("PAGE_TICKET") != "new-ticket" || v.Get("FLIGHT_ID_1") != "new-bound-2" || v.Get("RECOMMENDATION_ID_1") != "new-reco-2" {
			t.Fatal("recovery re-quoted completed flight or reused old booking state")
		}
		return &http.Response{StatusCode: 200}, awardTaxFlowResponse("new-ticket-2", "new-context-2", map[string]any{"Price": price, "ListItineraryView": view}), nil
	})
	if err := collectAwardTaxEstimates(context.Background(), c, pageData{SessionID: "new-context", PageTicket: "new-ticket"}, cash, &result, nil); err != nil || calls != 1 {
		t.Fatalf("fresh-context tax recovery failed: calls=%d err=%v", calls, err)
	}
	if !reflect.DeepEqual(result.Itineraries[0], firstBefore) || !result.Complete {
		t.Fatal("completed first estimate or award completeness changed during recovery")
	}
	estimate := result.Itineraries[1].Fares[0].TaxEstimate
	if estimate == nil || !equalCashAmounts(estimate.Total.Amount, "1.2") || len(estimate.Components) != 3 {
		t.Fatalf("second flight did not receive its own quote: %+v", estimate)
	}
}

func TestRetryAwardTaxLookupBoundedPolicy(t *testing.T) {
	for _, tt := range []struct {
		name      string
		failure   error
		wantCalls int
	}{
		{"connection", award.Failure("connection", "interrupted"), 2},
		{"response read", award.Failure("response_read", "interrupted"), 2},
		{"availability protocol", award.Failure("search_protocol", "missing availability"), 2},
		{"pricing protocol", award.Failure("pricing_protocol", "missing quotation state"), 2},
		{"denied", &award.Error{Code: "session_denied", HTTPStatus: 403}, 2},
		{"challenge rejected", award.Failure("challenge_rejected", "blocked"), 2},
		{"challenge service", award.Failure("challenge_service", "unavailable"), 2},
		{"temporary site", award.Failure("site_unavailable", "unavailable"), 2},
		{"upstream 503", &award.Error{Code: "provider_http", HTTPStatus: 503}, 2},
		{"wrapped renewal", &searchRenewal{cause: award.Failure("connection", "interrupted")}, 2},
		{"uncertain outcome", &award.Error{Code: "connection", OutcomeUnknown: true}, 1},
		{"credential transport", &award.Error{Code: "connection", Stage: "credentials"}, 1},
		{"OTP transport", &award.Error{Code: "response_read", Stage: "otp submission"}, 1},
		{"mailbox transport", &award.Error{Code: "connection", Stage: "mailbox"}, 1},
		{"upstream 400", &award.Error{Code: "provider_http", HTTPStatus: 400}, 1},
		{"rate limit", award.Failure("rate_limited", "pause"), 1},
		{"credentials rejected", award.Failure("login_response", "rejected"), 1},
		{"OTP", award.Failure("otp", "unavailable"), 1},
		{"mailbox", award.Failure("mailbox", "unavailable"), 1},
		{"tax breakdown invalid", award.Failure("tax_estimate_protocol", "invalid"), 1},
		{"flight mismatch", award.Failure("search_mismatch", "different flight"), 1},
		{"unknown error", errors.New("unclassified"), 1},
		{"context canceled", context.Canceled, 1},
		{"context deadline", context.DeadlineExceeded, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, _, _, _, _ := awardTaxFlowFixture(t)
			before := award.CloneResult(result)
			calls, recoveries := 0, 0
			err := retryAwardTaxLookup(context.Background(), &result, func() error { calls++; return tt.failure }, func(p award.Progress) {
				recoveries++
				if p.Stage != "award tax estimates" || p.Preview != &result || p.Recovery == nil || p.Recovery.Attempt != 1 {
					t.Fatal("tax retry did not expose a bounded recovery event")
				}
			})
			if err != tt.failure || calls != tt.wantCalls || recoveries != tt.wantCalls-1 || !reflect.DeepEqual(result, before) {
				t.Fatalf("retry policy violated: calls=%d recoveries=%d err=%v", calls, recoveries, err)
			}
		})
	}
}

func TestRetryAwardTaxLookupRetainsCompletedEstimatesAndSharedDeadline(t *testing.T) {
	result, _, _, _, _ := awardTaxFlowFixture(t)
	second := award.CloneResult(result).Itineraries[0]
	second.ID = "second-flight"
	result.Itineraries = append(result.Itineraries, second)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	deadline, _ := ctx.Deadline()
	calls := 0
	firstEstimate := &award.TaxEstimate{Total: award.Money{Amount: "6094", Currency: "TWD"}, Source: "eva_cash_fare", QuotedAt: time.Now().UTC()}
	err := retryAwardTaxLookup(ctx, &result, func() error {
		calls++
		if current, ok := ctx.Deadline(); !ok || !current.Equal(deadline) {
			t.Fatal("retry reset the shared deadline")
		}
		if calls == 1 {
			result.Itineraries[0].Fares[0].TaxEstimate = firstEstimate
			return fmt.Errorf("fresh availability failed: %w", award.Failure("search_protocol", "missing availability"))
		}
		if result.Itineraries[0].Fares[0].TaxEstimate != firstEstimate || !needsAwardTaxLookup(result) {
			t.Fatal("partial tax result lost before retry")
		}
		result.Itineraries[1].Fares[0].TaxEstimate = &award.TaxEstimate{Total: award.Money{Amount: "1.2", Currency: "TWD"}, Source: "eva_cash_fare", QuotedAt: time.Now().UTC()}
		return nil
	}, nil)
	if err != nil || calls != 2 || !result.Complete || needsAwardTaxLookup(result) || result.Itineraries[0].Fares[0].TaxEstimate.Total.Amount != "6094" || result.Itineraries[1].Fares[0].TaxEstimate.Total.Amount != "1.2" {
		t.Fatalf("partial retry failed: calls=%d err=%v", calls, err)
	}
}

func TestRetryAwardTaxLookupDoesNotRestartWhenAllEstimatesCompleted(t *testing.T) {
	result, _, _, _, _ := awardTaxFlowFixture(t)
	calls := 0
	failure := award.Failure("search_protocol", "late availability error")
	err := retryAwardTaxLookup(context.Background(), &result, func() error {
		calls++
		result.Itineraries[0].Fares[0].TaxEstimate = &award.TaxEstimate{Total: award.Money{Amount: "6094", Currency: "TWD"}}
		return failure
	}, func(award.Progress) { t.Fatal("unnecessary retry after every fare was estimated") })
	if err != failure || calls != 1 {
		t.Fatalf("finished estimates triggered more work: calls=%d err=%v", calls, err)
	}
}

func TestRetryAwardTaxLookupCancellationAndExpiredBudgetStopWork(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel between attempts", true: "already expired"}[expired], func(t *testing.T) {
			result, _, _, _, _ := awardTaxFlowFixture(t)
			var ctx context.Context
			var cancel context.CancelFunc
			if expired {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			calls := 0
			err := retryAwardTaxLookup(ctx, &result, func() error { calls++; return award.Failure("connection", "interrupted") }, func(award.Progress) { cancel() })
			want := context.Canceled
			wantCalls := 1
			if expired {
				want = context.DeadlineExceeded
				wantCalls = 0
			}
			if !errors.Is(err, want) || calls != wantCalls {
				t.Fatalf("budget/cancellation ignored: calls=%d err=%v", calls, err)
			}
		})
	}
}
