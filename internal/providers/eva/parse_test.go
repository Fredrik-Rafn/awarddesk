package eva

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"awarddesk/internal/award"
)

func TestCapturedAvailabilityAllFlights(t *testing.T) {
	raw, err := os.ReadFile("testdata/availability.json")
	if err != nil {
		t.Fatal(err)
	}
	q := award.Query{Provider: "eva", Origin: "TPE", Destination: "HKG", Date: "2026-11-24", Cabin: award.Economy, Adults: 1}
	for _, tc := range []struct {
		c award.Cabin
		n int
	}{{award.Economy, 10}, {award.Business, 7}} {
		q.Cabin = tc.c
		r, err := parseAvailability(raw, q)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Itineraries) != tc.n {
			t.Fatalf("%s: got %d flights", tc.c, len(r.Itineraries))
		}
		if r.Complete {
			t.Fatal("missing mileage conversion must remain partial")
		}
		for _, i := range r.Itineraries {
			for _, f := range i.Fares {
				if f.PricingType != "miles_cash" || f.CashPrice == nil || f.CashPrice.Currency != "TWD" || f.MilesCash != nil {
					t.Fatal("cash price mislabeled as award quote")
				}
				if strings.Contains(f.Name, "<") {
					t.Fatal("HTML in fare name")
				}
			}
		}
	}
	q.Origin = "LAX"
	if _, err := parseAvailability(raw, q); err == nil {
		t.Fatal("accepted different route")
	}
}
func TestDecodePageDoesNotExecuteScript(t *testing.T) {
	for _, body := range []string{
		`{"data":{"pageTicket":"2","business":{"Availability":{}}},"sessionId":"test-session"}`,
		`<script>init({config : {"siteConfiguration":{"appData":{"beSessionId":"analytics-session"}},"pageDefinitionConfig":{"pageData":{"pageTicket":"2","business":{"Availability":{}}}}}, sessionId : "test-session"}); malicious();</script>`,
	} {
		p, err := decodePage([]byte(body))
		if err != nil || p.PageTicket != "2" || p.SessionID != "test-session" || !json.Valid(p.Business["Availability"]) {
			t.Fatal("bootstrap not parsed")
		}
	}
	if _, err := decodePage([]byte(`<script>fetch("secret")</script>`)); err == nil {
		t.Fatal("accepted missing data")
	}
}

func TestMilesCashEligibility(t *testing.T) {
	raw, err := os.ReadFile("testdata/availability.json")
	if err != nil {
		t.Fatal(err)
	}
	var captured availability
	if err = json.Unmarshal(raw, &captured); err != nil {
		t.Fatal(err)
	}
	// Booking class A is excluded even on an EVA-operated flight.
	captured.RecommendationList[0].Bounds[0].FlightGroupList[0].RBD = "A"
	raw, _ = json.Marshal(captured)
	r, err := parseAvailability(raw, award.Query{Provider: "eva", Origin: "TPE", Destination: "HKG", Date: "2026-11-24", Cabin: award.Economy, Adults: 1})
	if err != nil {
		t.Fatal(err)
	}
	excluded := 0
	for _, item := range r.Itineraries {
		for _, fare := range item.Fares {
			if fare.MilesCashEligible != nil && !*fare.MilesCashEligible {
				excluded++
			}
		}
	}
	// Three codeshare flights with three fare families, plus class A above.
	if excluded != 10 {
		t.Fatalf("expected ten ineligible fare/flight combinations, got %d", excluded)
	}
}
