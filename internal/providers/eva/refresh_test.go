package eva

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"

	"awarddesk/internal/award"
	"awarddesk/internal/session"
)

func TestRefreshPreservesQueryAndUsesCurrentSession(t *testing.T) {
	raw, err := os.ReadFile("testdata/availability.json")
	if err != nil {
		t.Fatal(err)
	}
	q := award.Query{Provider: "eva", Origin: "TPE", Destination: "HKG", Date: "2026-11-24", Cabin: award.Economy, Adults: 1}
	c := pricingTransport(func(ctx context.Context, method, target string, body io.Reader, h http.Header) (*http.Response, []byte, error) {
		if method != "POST" || target != "https://book.evaair.com/plnext/EVAOnlineDX/FlexPricerAvailability.action;jsessionid=current" {
			t.Fatal("refresh left current booking session")
		}
		b, _ := io.ReadAll(body)
		v, _ := url.ParseQuery(string(b))
		if v.Get("B_LOCATION_1") != "TPE" || v.Get("E_LOCATION_1") != "HKG" || v.Get("B_DATE_1") != "202611240000" || v.Get("PAGE_TICKET") != "current-ticket" || v.Get("DATE_RANGE_VALUE_1") != "0" || v.Get("ACTION") != "" || v.Get("DATE_OF_BIRTH_1") != "" {
			t.Fatal("refresh used stale or unrelated parameters")
		}
		r, _ := json.Marshal(map[string]any{"data": map[string]any{"pageDefinitionConfig": map[string]any{"pageData": map[string]any{"pageTicket": "refreshed", "business": map[string]any{"Availability": json.RawMessage(raw)}}}}, "sessionId": "fresh-session"})
		return &http.Response{StatusCode: 200}, r, nil
	})
	p, r, err := refreshAvailability(context.Background(), c, pageData{SessionID: "current", PageTicket: "current-ticket"}, pageData{SessionID: "old", Basefacts: map[string]string{"request.DATE_OF_BIRTH_1": "must-not-copy", "request.B_DATE_1": "stale"}}, q)
	if err != nil {
		t.Fatal(err)
	}
	if p.SessionID != "fresh-session" || len(r.Itineraries) != 10 {
		t.Fatal("fresh availability state was lost")
	}
}

func TestRefreshedFareMatchedByFlightNotOldIdentifier(t *testing.T) {
	raw, _ := os.ReadFile("testdata/availability.json")
	r, err := parseAvailability(raw, award.Query{Origin: "TPE", Destination: "HKG", Date: "2026-11-24", Cabin: award.Economy})
	if err != nil {
		t.Fatal(err)
	}
	wanted := r.Itineraries[0]
	wanted.Segments = append([]award.Segment(nil), wanted.Segments...)
	fare := wanted.Fares[0]
	r.Itineraries[0].ID = "42"
	r.Itineraries[0].Fares[0].QuoteID = "73"
	flight, reco, err := matchingSelection(r, wanted, fare)
	if err != nil || flight != "42" || reco != "73" {
		t.Fatal("refreshed identifiers not used")
	}
	r.Itineraries[0].Segments[0].FlightNumber = "BR999"
	if _, _, err := matchingSelection(r, wanted, fare); err == nil {
		t.Fatal("quote assigned to a different flight")
	}
}

func TestAvailabilityReadRecoversMissingPageWithoutReplayingCheckout(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		t.Run(strconv.FormatBool(permanent), func(t *testing.T) {
			calls, retries := 0, 0
			raw, _ := os.ReadFile("testdata/availability.json")
			c := pricingTransport(func(ctx context.Context, method, target string, body io.Reader, h http.Header) (*http.Response, []byte, error) {
				calls++
				if !strings.Contains(target, "FlexPricerAvailability.action") {
					t.Fatal("availability retry left read-only endpoint")
				}
				if calls == 1 || permanent {
					return &http.Response{StatusCode: 200}, []byte("<html>Temporarily unavailable</html>"), nil
				}
				b, _ := json.Marshal(map[string]any{"data": pageData{PageTicket: "fresh", Business: map[string]json.RawMessage{"Availability": raw}}})
				return &http.Response{StatusCode: 200}, b, nil
			})
			ctx := session.WithRetryObserver(context.Background(), func(error, int) { retries++ })
			_, _, err := refreshAvailability(ctx, c, pageData{SessionID: "current"}, pageData{}, award.Query{Origin: "TPE", Destination: "HKG", Date: "2026-11-24", Cabin: award.Economy})
			if (err != nil) != permanent || calls != 2 || retries != 1 {
				t.Fatalf("retry result: err=%v calls=%d retries=%d", err, calls, retries)
			}
		})
	}
}

func TestAvailabilityReadDoesNotRetryAccountOrRateLimitErrors(t *testing.T) {
	for _, err := range []error{award.Failure("authentication", "denied"), award.Failure("rate_limited", "pause"), award.Failure("price_changed", "changed"), context.Canceled} {
		if canRetryAvailabilityRead(err) {
			t.Fatalf("unexpected availability retry: %v", err)
		}
	}
}
