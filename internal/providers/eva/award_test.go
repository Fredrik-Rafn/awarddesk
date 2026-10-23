package eva

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"awarddesk/internal/award"
)

func awardFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/award-" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func awardQuery() award.Query {
	return award.Query{Provider: "eva", Origin: "SIN", Destination: "TPE", Date: "2026-11-24", Cabin: award.Economy, Adults: 4, Mode: "award"}
}

func TestAwardAvailabilityUsesPartyAndDateNotWaitlist(t *testing.T) {
	q := awardQuery()
	b := awardFixture(t, "availability.html")
	available, err := parseAwardAvailability(b, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(available) != 1 || !available["BR216"] {
		t.Fatalf("unexpected availability: %v", available)
	}
	q.Date = "2026-11-25"
	available, err = parseAwardAvailability(b, q)
	if err != nil || len(available) != 0 {
		t.Fatalf("waitlist became availability: %v %v", available, err)
	}
	for _, change := range []func(*award.Query){func(q *award.Query) { q.Adults = 1 }, func(q *award.Query) { q.Origin = "HKG" }, func(q *award.Query) { q.Cabin = award.Business }, func(q *award.Query) { q.Date = "2026-12-25" }} {
		bad := awardQuery()
		change(&bad)
		if _, err = parseAwardAvailability(b, bad); err == nil {
			t.Fatal("mismatched query accepted")
		}
	}
	unknown := strings.ReplaceAll(string(b), `alt="Available"`, `alt="Unknown"`)
	if _, err = parseAwardAvailability([]byte(unknown), awardQuery()); err == nil {
		t.Fatal("unknown availability accepted")
	}
}

func TestAwardExplicitUnservedCabinIsEmptyButMissingGridIsNot(t *testing.T) {
	q := awardQuery()
	base := string(awardFixture(t, "availability-form.html"))
	f, err := parseForm([]byte(base), awardAvailabilityURL)
	if err != nil {
		t.Fatal(err)
	}
	if err = fillAwardAvailability(&f, q); err != nil {
		t.Fatal(err)
	}
	var hidden strings.Builder
	hidden.WriteString(`<form><div id="mainContent">`)
	for name, values := range f.values {
		for _, value := range values {
			hidden.WriteString(`<input name="` + name + `" value="` + value + `">`)
		}
	}
	body := hidden.String() + `<p>The class of service you want to book is not available on this route or we do not have a flight on the day you want to travel.</p></div></form>`
	available, err := parseAwardAvailability([]byte(body), q)
	if err != nil || len(available) != 0 {
		t.Fatalf("explicit empty response rejected: %v", err)
	}
	iso := strings.ReplaceAll(body, "2026/11/24", "2026-11-24")
	if _, err = parseAwardAvailability([]byte(iso), q); err != nil {
		t.Fatal("ISO empty response rejected", err)
	}
	if _, err = parseAwardAvailability([]byte(hidden.String()+`</div></form>`), q); err == nil {
		t.Fatal("unexplained missing grid became empty success")
	}
	q.Date = "2026-11-25"
	if _, err = parseAwardAvailability([]byte(body), q); err == nil {
		t.Fatal("empty response for another date accepted")
	}
}

func TestAwardDatesHandleStoredAccountMarketFormats(t *testing.T) {
	for _, value := range []string{"24 Nov.2026Tuesday", "Nov. 24, 2026Tuesday", "Nov. 24, 2026 15:45", "24 Nov.2026 15:45"} {
		clock := strings.Contains(value, ":")
		got, ok := parseAwardDate(value, clock)
		want := "2026-11-24"
		if clock {
			want += "T15:45:00"
		}
		if !ok || got != want {
			t.Fatalf("%q = %q %v", value, got, ok)
		}
	}
}

func TestAwardFlightNumbersMatchPaddingAndSpacing(t *testing.T) {
	if awardFlightKey([]string{"BR001", "B7 007"}) != awardFlightKey([]string{"BR1", "B77"}) {
		t.Fatal("flight formatting changed identity")
	}
	q := awardQuery()
	q.Date = time.Now().AddDate(0, 0, 361).Format("2006-01-02")
	if q.Validate(time.Now()) == nil {
		t.Fatal("award date outside booking window accepted")
	}
	q.Mode = ""
	if q.Validate(time.Now()) != nil {
		t.Fatal("award booking window affected original flow")
	}
}

func TestAwardMixedCabinSegmentsKeepTheirActualCabins(t *testing.T) {
	segment := func(from, to, flight, cabin, departure, arrival string) string {
		return `<div class="trafficInformation--flightsItem"><div class="flightSegment-airport--mini">` + from + `<span>24 Nov.2026 ` + departure + `</span></div><div class="flightSegment-airport--mini">` + to + `<span>24 Nov.2026 ` + arrival + `</span></div><dl><dt>Flight Number</dt><dd>` + flight + `</dd><dt>Class</dt><dd>` + cabin + `</dd></dl></div>`
	}
	first := segment("HND", "TPE", "BR189", "Business", "10:00", "13:00")
	second := segment("TPE", "SIN", "BR225", "Economy", "14:00", "18:00")
	root, _ := awardDocument([]byte(first + second))
	segments, err := parseAwardSegments(root, award.Business)
	if err != nil || len(segments) != 2 || segments[1].Cabin != award.Economy {
		t.Fatalf("mixed cabin: %v %v", segments, err)
	}
	if _, err = parseAwardSegments(root, award.Economy); err == nil {
		t.Fatal("higher cabin substituted for requested economy")
	}
}

func TestAwardOfferedAirportChangeRemainsAnItinerary(t *testing.T) {
	q := awardQuery()
	q.Origin = "HND"
	q.Destination = "SIN"
	q.Date = "2027-01-15"
	available := map[string]bool{"BR191/BR225": true, "BR189/BR225": true, "BR191/BR215": true, "BR189/BR215": true}
	choices, err := parseAwardChoices(awardFixture(t, "connection.html"), q, available)
	if err != nil || len(choices) != 4 {
		t.Fatalf("airport-change options lost: %d %v", len(choices), err)
	}
	s := choices[0].itinerary.Segments
	if len(s) != 2 || s[0].Destination != "TSA" || s[1].Origin != "TPE" || s[1].DepartureLocal != "2027-01-16T07:25:00" {
		t.Fatalf("surface transfer mislabeled: %+v", s)
	}
}

func TestAwardChoicesAndMileageMatchActualFlight(t *testing.T) {
	q := awardQuery()
	b := awardFixture(t, "journeys.html")
	choices, err := parseAwardChoices(b, q, map[string]bool{"BR216": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(choices) != 1 || choices[0].itinerary.Segments[0].FlightNumber != "BR216" {
		t.Fatalf("wrong flights: %#v", choices)
	}
	quote := awardFixture(t, "mileage.html")
	points, err := parseAwardMileage(quote, choices[0], q)
	if err != nil || points != 70000 {
		t.Fatalf("party miles=%d: %v", points, err)
	}
	f, err := parseForm(b, awardJourneyURL)
	if err != nil {
		t.Fatal(err)
	}
	if err = selectAwardChoice(&f, choices[0]); err != nil {
		t.Fatal(err)
	}
	if f.values.Get(choices[0].radioName) != "rb_Choice" || f.values.Get(f.field("__MasterEVENTTARGET")) != "btn_Confirm_Click" {
		t.Fatal("wrong selection postback")
	}
	for _, bad := range []string{strings.ReplaceAll(string(quote), "BR216", "BR226"), strings.ReplaceAll(string(quote), "15:45", "15:50"), strings.ReplaceAll(string(quote), "17,500", "unknown"), strings.ReplaceAll(string(quote), "Economy", "Business")} {
		if _, err = parseAwardMileage([]byte(bad), choices[0], q); err == nil {
			t.Fatal("mismatched mileage quote accepted")
		}
	}
}

func TestAwardSearchSkipsPricingForNoAvailabilityAndKeepsModesSeparate(t *testing.T) {
	q := awardQuery()
	q.Date = "2026-11-25"
	p := New(Config{})
	calls := 0
	r, err := p.runAwardSearch(context.Background(), q, func(_ context.Context, method, target string, values url.Values, from string) ([]byte, string, error) {
		calls++
		if target != awardAvailabilityURL {
			t.Fatalf("unnecessary pricing call: %s", target)
		}
		if method == "GET" {
			return awardFixture(t, "availability-form.html"), target, nil
		}
		if values.Get("ctl00$content$ddl_PassengerCount") != "4" {
			t.Fatal("party size was not checked")
		}
		return awardFixture(t, "availability.html"), target, nil
	}, nil)
	if err != nil || !r.Complete || len(r.Itineraries) != 0 || calls != 2 {
		t.Fatalf("empty result: %+v calls=%d err=%v", r, calls, err)
	}
	other := q
	other.Mode = ""
	if p.resultStore(q).Path == p.resultStore(other).Path {
		t.Fatal("award and Miles & Cash share a cache key")
	}
	q.Date = time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	q.Adults = 7
	if q.Validate(time.Now()) == nil {
		t.Fatal("unsupported award party accepted")
	}
	other = q
	other.Mode = ""
	if other.Validate(time.Now()) != nil {
		t.Fatal("original adult limit changed")
	}
}

func TestAwardSearchReplaysProtocolWithoutBookingOrSolvers(t *testing.T) {
	p := New(Config{})
	q := awardQuery()
	calls := 0
	r, err := p.runAwardSearch(context.Background(), q, func(_ context.Context, method, target string, values url.Values, from string) ([]byte, string, error) {
		calls++
		switch {
		case method == "GET" && target == awardAvailabilityURL:
			return awardFixture(t, "availability-form.html"), target, nil
		case method == "POST" && target == awardAvailabilityURL:
			if values.Get("ctl00$content$ddl_PassengerCount") != "4" || values.Get("ctl00$content$txt_tbGo2YYYYMM") != "2026/11/24" {
				t.Fatal("wrong availability query")
			}
			return awardFixture(t, "availability.html"), target, nil
		case target == awardAirportURL:
			return awardFixture(t, "airports.xml"), target, nil
		case method == "GET" && target == awardItineraryURL:
			return awardFixture(t, "itinerary.html"), target, nil
		case method == "POST" && strings.HasPrefix(target, awardItineraryURL):
			if values.Get("ctl00$content$hid_DepCommunityCode") != "03" || values.Get("ctl00$content$hid_ArrCommunityCode") != "01" || values.Get("ctl00$content$ddl_Cabin") != "X" {
				t.Fatal("wrong booking metadata")
			}
			return []byte(`<form action="select-first-journey.aspx"><input name="Parameter" value="fresh-handoff"></form>`), target, nil
		case allowedAwardPage(target, "select-first-journey.aspx") && values.Has("Parameter"):
			return awardFixture(t, "journeys.html"), target, nil
		case allowedAwardPage(target, "select-first-journey.aspx"):
			if values.Get("ctl00$__MasterEVENTTARGET") != "btn_Confirm_Click" {
				t.Fatal("wrong selection")
			}
			return []byte(`<form action="mileage-deducted.aspx"><input name="Parameter" value="fresh-quote"></form>`), target, nil
		case allowedAwardPage(target, "mileage-deducted.aspx"):
			return awardFixture(t, "mileage.html"), target, nil
		default:
			t.Fatalf("unexpected request: %s %s", method, target)
			return nil, target, nil
		}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 8 || !r.Complete || len(r.Itineraries) != 1 || r.Itineraries[0].Fares[0].Points != 70000 {
		t.Fatalf("result=%+v requests=%d", r, calls)
	}
	f := r.Itineraries[0].Fares[0]
	if f.Seats != nil || f.Taxes.Amount != "" || f.AvailableFor != 4 || f.Estimated || f.PricingType != "award" {
		t.Fatal("invented seat count, tax quote or wrong pricing type")
	}
}

func TestAwardBridgeRejectsUnexpectedDestinations(t *testing.T) {
	for _, action := range []string{"https://example.com/mileage-deducted.aspx", "passenger-information.aspx", "https://booking.evaair.com@evil.example/mileage-deducted.aspx"} {
		_, _, err := followAwardBridge(context.Background(), []byte(`<form action="`+action+`"><input name="Parameter" value="opaque"></form>`), awardJourneyURL, "mileage-deducted.aspx", func(context.Context, string, string, url.Values, string) ([]byte, string, error) {
			t.Fatal("unsafe destination requested")
			return nil, "", nil
		})
		if err == nil {
			t.Fatal("unsafe bridge accepted")
		}
	}
}

func TestAwardMultipleFlightsUseFreshSelectionStateAndSeparateQuotes(t *testing.T) {
	p := New(Config{})
	q := awardQuery()
	availability := strings.ReplaceAll(string(awardFixture(t, "availability.html")), `alt="Ｗaiting"`, `alt="Available"`)
	journeys := strings.ReplaceAll(string(awardFixture(t, "journeys.html")), ">waiting<", ">Available(Unconfirmed)<")
	selection, reopened := 0, false
	r, err := p.runAwardSearch(context.Background(), q, func(_ context.Context, method, target string, values url.Values, from string) ([]byte, string, error) {
		switch {
		case target == awardAvailabilityURL && method == "GET":
			return awardFixture(t, "availability-form.html"), target, nil
		case target == awardAvailabilityURL:
			return []byte(availability), target, nil
		case target == awardAirportURL:
			return awardFixture(t, "airports.xml"), target, nil
		case method == "GET" && target == awardItineraryURL:
			return awardFixture(t, "itinerary.html"), target, nil
		case allowedAwardPage(target, "select-itinerary.aspx"):
			return []byte(`<form action="select-first-journey.aspx"><input name="Parameter" value="fresh"></form>`), target, nil
		case allowedAwardPage(target, "select-first-journey.aspx") && values.Has("Parameter"):
			return []byte(journeys), target, nil
		case method == "GET" && target == awardJourneyURL:
			reopened = true
			return []byte(strings.ReplaceAll(journeys, "fresh-test-state", "refreshed-state")), target, nil
		case allowedAwardPage(target, "select-first-journey.aspx"):
			selection++
			if selection == 2 && (!reopened || values.Get("__ZIPSTATE") != "refreshed-state") {
				t.Fatal("reused stale selection state")
			}
			return []byte(`<form action="mileage-deducted.aspx"><input name="Parameter" value="fresh"></form>`), target, nil
		case allowedAwardPage(target, "mileage-deducted.aspx"):
			quote := string(awardFixture(t, "mileage.html"))
			if selection == 1 {
				quote = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(quote, "BR216", "BR226"), "15:45", "13:15"), "20:15", "17:50")
			}
			if selection == 2 {
				quote = strings.ReplaceAll(quote, "17,500", "20,000")
			}
			return []byte(quote), target, nil
		default:
			t.Fatalf("unexpected request %s %s", method, target)
			return nil, target, nil
		}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Complete || len(r.Itineraries) != 2 || selection != 2 || r.Itineraries[0].Fares[0].Points != 70000 || r.Itineraries[1].Fares[0].Points != 80000 {
		t.Fatalf("quotes incorrectly reused: %+v", r)
	}
}
