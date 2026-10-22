package ba

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"awarddesk/internal/award"
)

func TestCapturedAvailability(t *testing.T) {
	b, e := os.ReadFile("testdata/availability.html")
	if e != nil {
		t.Fatal(e)
	}
	got, e := parseAwards(b, "M")
	if e != nil {
		t.Fatal(e)
	}
	if len(got) != 13 {
		t.Fatalf("got %d itineraries", len(got))
	}
	direct := 0
	for _, a := range got {
		if len(a.Segments) == 1 {
			direct++
		}
		if a.Selection == "" || len(a.Segments) == 0 {
			t.Fatal("incomplete flight")
		}
		for _, s := range a.Segments {
			if s.Flight == "" || s.Origin == "" || s.Destination == "" || s.DepartureTime == "" {
				t.Fatalf("incomplete segment %+v", s)
			}
		}
	}
	if direct != 8 {
		t.Fatalf("got %d direct flights", direct)
	}
}

func TestFlightDetails(t *testing.T) {
	d := parseDetail([]byte(`<table><tr><th>Aircraft type:</th><td>Boeing 777</td></tr><tr><th>Operated by</th><td>American Airlines</td></tr></table>`))
	if d.aircraft == nil || *d.aircraft != "Boeing 777" || d.operator != "American Airlines" {
		t.Fatalf("details %+v", d)
	}
	if parseDetail([]byte(`<p>No details available</p>`)).aircraft != nil {
		t.Fatal("invented aircraft")
	}
	repeated := parseDetail([]byte(`<table><tr><th>Aircraft type:</th><td><span>Aircraft type:</span> Airbus A321 Neo</td></tr></table>`))
	if repeated.aircraft == nil || *repeated.aircraft != "Airbus A321 Neo" {
		t.Fatal("accessibility label leaked into aircraft value")
	}
	if parseDetail([]byte(`<div><span>Aircraft type:</span></div><div>Aircraft type:</div>`)).aircraft != nil {
		t.Fatal("field label mistaken for aircraft")
	}
	for _, raw := range []string{"https://evil.example/?eId=100023", awardBase + "?eId=100004", "javascript:alert(1)"} {
		if _, ok := detailURL(raw); ok {
			t.Fatal("unsafe detail link accepted")
		}
	}
	if _, ok := detailURL(awardBase + "?eId=100023&SegmentNumber=0"); !ok {
		t.Fatal("expected detail URL rejected")
	}
}

func TestCabinAvailabilityAndNormalization(t *testing.T) {
	for _, c := range []award.Cabin{award.Economy, award.PremiumEconomy, award.Business, award.First} {
		t.Run(string(c), func(t *testing.T) {
			body := fmt.Sprintf(`<div class="journeyOptions"><table summary="One stop"><tr><td><div class="travel-time-detail"><span class="departtime">23:30</span><span class="departdate">31 Dec</span><span class="arrivaltime">06:00</span><span class="arrivaldate">1 Jan</span><a id="deptAirportCodeLink0">JFK</a><a id="arrivalAirportCodeLink0">LHR</a><a class="flightDetailModal"><span class="sl_split">British Airways</span><span class="sl_split">BA178</span></a></div><div class="cabin-data"><input class="cabinSelector" id="optionCabinCode%s" value="fresh-selection"><span aria-label="2 seats left"></span></div></td></tr></table></div>`, cabinCode(c))
			got, e := parseAwards([]byte(body), cabinCode(c))
			if e != nil || len(got) != 1 {
				t.Fatalf("availability: %v %v", got, e)
			}
			if got[0].SeatsShown == nil || *got[0].SeatsShown != 2 || got[0].Selection != "fresh-selection" {
				t.Fatal("availability fields missing")
			}
			q := award.Query{Cabin: c}
			it := normalize(got[0], q, time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
			if len(it.Segments) != 1 || it.Segments[0].ArrivalLocal != "2027-01-01T06:00:00" || it.Segments[0].Aircraft != nil {
				t.Fatalf("normalization: %+v", it)
			}
			if _, e = parseAwards([]byte(body), "X"); e == nil {
				t.Fatal("wrong cabin silently accepted")
			}
		})
	}
}
func TestQuotesAllOptionsAndMissing(t *testing.T) {
	b := []byte(`<div class="totalPriceAviosTxt">22,000 Avios + 37.00 DKK</div><ul id="lstMrePrcngSB"><li>22,000 Avios + 37.00 DKK</li><li>18,000 Avios + 300.00 DKK</li></ul><input id="hdnMrePrceCnt" value="2">`)
	f, missing := fares(b, award.Economy, nil)
	if missing || len(f) != 2 || f[0].Points != 22000 || f[1].Taxes.Amount != "300.00" || f[0].Taxes.Currency != "DKK" {
		t.Fatalf("bad quotes %+v %v", f, missing)
	}
	_, missing = fares([]byte(strings.ReplaceAll(string(b), `value="2"`, `value="3"`)), award.Economy, nil)
	if !missing {
		t.Fatal("hidden options must be disclosed")
	}
	f, _ = fares([]byte(`<div>Cash fare 100 DKK</div>`), award.First, nil)
	if len(f) != 0 {
		t.Fatal("cash must not be an award")
	}
}
func TestFreshContinuation(t *testing.T) {
	b := []byte(`<form name="SubmitFromInterstitial"><input type="hidden" name="FormToken" value=""><input type="hidden" name="eId" value=""></form><script>var eventId='111011';</script>`)
	v, e := continuationFields(b)
	if e != nil || v.Get("eId") != "111011" || v.Get("FormToken") != "" {
		t.Fatalf("continuation %v %v", v, e)
	}
	if _, e = continuationFields([]byte(strings.ReplaceAll(string(b), "111011", "100004"))); e == nil {
		t.Fatal("unexpected event accepted")
	}
	if _, e = parseAwards([]byte(`<h1>High demand</h1>`), "M"); e == nil {
		t.Fatal("error page treated as no inventory")
	}
}
