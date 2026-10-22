package eva

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"

	"awarddesk/internal/award"
)

func TestPartyCashAmountUsesExactDecimalArithmetic(t *testing.T) {
	for _, tc := range []struct {
		amount string
		adults int
		want   string
	}{
		{"38020", 2, "76040"}, {"18373", 3, "55119"}, {"0.10", 3, "0.30"},
		{"123.45", 9, "1111.05"}, {"6.5e-2", 2, "0.130"}, {"5183.0", 1, "5183.0"},
	} {
		got, err := partyCashAmount(tc.amount, tc.adults)
		if err != nil || got != tc.want {
			t.Fatalf("%s x %d = %s, err %v", tc.amount, tc.adults, got, err)
		}
	}
	for _, raw := range []string{"-1", "NaN", "1/2", "1e-99999999", "null", ""} {
		if _, err := partyCashAmount(raw, 2); err == nil {
			t.Fatalf("invalid amount accepted: %q", raw)
		}
	}
	if !equalCashAmounts("0.30", "0.3") || equalCashAmounts("76040", "76041") {
		t.Fatal("cash comparison lost exact totals")
	}
}

func TestGroupPricingPreservesAllTravellersAcrossFareRefresh(t *testing.T) {
	for _, adults := range []int{1, 2, 3, 9} {
		t.Run(strconv.Itoa(adults), func(t *testing.T) {
			q := award.Query{Provider: "eva", Origin: "TPE", Destination: "HKG", Date: "2026-11-24", Cabin: award.Economy, Adults: adults}
			r, err := parseAvailability(recoveryAvailability(200), q)
			if err != nil {
				t.Fatal(err)
			}
			bookings, refreshes := 0, 0
			c := pricingTransport(func(ctx context.Context, method, target string, body io.Reader, h http.Header) (*http.Response, []byte, error) {
				b, _ := io.ReadAll(body)
				v, _ := url.ParseQuery(string(b))
				business := map[string]json.RawMessage{}
				switch {
				case strings.Contains(target, "FlexPricerAvailability"):
					refreshes++
					for n := 1; n <= adults; n++ {
						if v.Get("TRAVELLER_TYPE_"+strconv.Itoa(n)) != "ADT" {
							t.Fatal("refresh dropped a traveller")
						}
					}
					if v.Get("TRAVELLER_TYPE_"+strconv.Itoa(adults+1)) != "" {
						t.Fatal("extra refresh traveller")
					}
					business["Availability"] = recoveryAvailability(200)
				case strings.Contains(target, "AjaxCall"):
					amount := 100 * adults
					if v.Get("RECOMMENDATION_ID_1") == "2" {
						amount = 200 * adults
					}
					business["Price"] = json.RawMessage(fmt.Sprintf(`{"totalAmount":{"amount":%d,"currency":{"code":"TWD"}}}`, amount))
				case strings.Contains(target, "AddElementsTravellerInformation"):
					list := `{"Travellers":[{"Type":{"code":"ADT"},"IdentityInformation":{"IDEN_TitleCode":"MR","IDEN_FirstName":"TEST","IDEN_LastName":"MEMBER","IDEN_DateOfBirth":"1990/05/09","IDEN_Nationality":"TW"}}`
					for n := 1; n < adults; n++ {
						list += `,{"Type":{"code":"ADT"},"IdentityInformation":{}}`
					}
					list += `]}`
					business["TravellerList"] = json.RawMessage(list)
					business["ContactInformation"] = json.RawMessage(`{"Email":"test@example.test","PhoneMobile":"123456789","PhoneMobileCode":"886"}`)
				case strings.Contains(target, "AddElementsDynamicCatalog"):
					for n := 1; n <= adults; n++ {
						if v.Get("FIRST_NAME_"+strconv.Itoa(n)) == "" {
							t.Fatal("missing traveller profile")
						}
					}
				case strings.Contains(target, "AddDynamicCatalogSelectionToTripPlan"):
					bookings++
					business["MilesAndCashView"] = json.RawMessage(`{"maxRankAvailable":{"milesAmount":1000,"nonConvertedAmount":{"amount":50}}}`)
				default:
					t.Fatal("unexpected endpoint")
				}
				data, _ := json.Marshal(map[string]any{"data": pageData{PageTicket: "fresh", Business: business}, "sessionId": "session"})
				return &http.Response{StatusCode: 200}, data, nil
			})
			err = priceFaresWithReplans(context.Background(), c, pageData{SessionID: "session", PageTicket: "initial"}, &r, nil, 2)
			if err != nil || !r.Complete || bookings != 2 || refreshes != 1 {
				t.Fatalf("group pricing failed: %v bookings=%d refreshes=%d", err, bookings, refreshes)
			}
			if r.Itineraries[0].Fares[0].CashPrice.Amount != strconv.Itoa(100*adults) || r.Itineraries[0].Fares[0].MilesCash.Miles != 1000 {
				t.Fatal("party cash or provider mileage quote was scaled incorrectly")
			}
		})
	}
}

func TestGenuineGroupPriceChangeStillStopsBeforeCheckout(t *testing.T) {
	q := award.Query{Origin: "TPE", Destination: "HKG", Date: "2026-11-24", Cabin: award.Economy, Adults: 2}
	r, _ := parseAvailability(recoveryAvailability(200), q)
	c := pricingTransport(func(ctx context.Context, m, target string, b io.Reader, h http.Header) (*http.Response, []byte, error) {
		if !strings.Contains(target, "AjaxCall") {
			t.Fatal("changed fare reached checkout")
		}
		return &http.Response{StatusCode: 200}, []byte(`{"data":{"pageTicket":"next","business":{"Price":{"totalAmount":{"amount":201,"currency":{"code":"TWD"}}}}}}`), nil
	})
	err := priceFaresWithReplans(context.Background(), c, pageData{SessionID: "session"}, &r, nil, 2)
	var failure *award.Error
	if !errors.As(err, &failure) || failure.Code != "price_changed" {
		t.Fatalf("real price change accepted: %v", err)
	}
}
