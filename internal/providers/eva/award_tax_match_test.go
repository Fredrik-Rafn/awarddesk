package eva

import (
	"encoding/json"
	"reflect"
	"testing"

	"awarddesk/internal/award"
)

func cashTaxMatchFixture() (award.Query, award.Itinerary, award.Result) {
	q := award.Query{Provider: "eva", Mode: "award", Origin: "TPE", Destination: "HKG", Date: "2026-11-27", Cabin: award.Economy, Adults: 2, Refresh: true}
	target := award.Itinerary{ID: "award", Segments: []award.Segment{{Cabin: q.Cabin, FlightNumber: "BR001", Airline: "EVA Air", Origin: q.Origin, Destination: q.Destination, DepartureLocal: q.Date + "T08:00:00", ArrivalLocal: q.Date + "T10:00:00"}}}
	cashQuery := q
	cashQuery.Mode, cashQuery.Refresh = "", false
	segment := target.Segments[0]
	segment.Cabin, segment.FlightNumber, segment.OperatingAirline = "", "BR 1", "EVA Airways"
	eligible := false
	fare := award.Fare{Name: "Economy Basic", Cabin: q.Cabin, QuoteID: "fresh-quote", CashPrice: &award.Money{Amount: "100.50", Currency: "TWD"}, MilesCashEligible: &eligible}
	cash := award.Result{Query: cashQuery, Itineraries: []award.Itinerary{{ID: "fresh-bound", Segments: []award.Segment{segment}, Fares: []award.Fare{fare}}}}
	return q, target, cash
}

func TestCashTaxCandidateMatchesCanonicalFlightsWithoutMilesCashEligibility(t *testing.T) {
	q, target, cash := cashTaxMatchFixture()
	itinerary, fare, ok := cashTaxCandidate(q, target, cash)
	if !ok || !reflect.DeepEqual(itinerary, cash.Itineraries[0]) || !reflect.DeepEqual(fare, cash.Itineraries[0].Fares[0]) {
		t.Fatalf("exact cash candidate excluded: ok=%v itinerary=%+v fare=%+v", ok, itinerary, fare)
	}
}

func TestCashTaxCandidateRejectsQueryAndFlightMismatches(t *testing.T) {
	for name, change := range map[string]func(*award.Query, *award.Itinerary, *award.Result){
		"provider":               func(_ *award.Query, _ *award.Itinerary, r *award.Result) { r.Query.Provider = "other" },
		"cash mode":              func(_ *award.Query, _ *award.Itinerary, r *award.Result) { r.Query.Mode = "award" },
		"award mode":             func(q *award.Query, _ *award.Itinerary, _ *award.Result) { q.Mode = "" },
		"origin query":           func(_ *award.Query, _ *award.Itinerary, r *award.Result) { r.Query.Origin = "SIN" },
		"destination query":      func(_ *award.Query, _ *award.Itinerary, r *award.Result) { r.Query.Destination = "SIN" },
		"date query":             func(_ *award.Query, _ *award.Itinerary, r *award.Result) { r.Query.Date = "2026-11-28" },
		"party query":            func(_ *award.Query, _ *award.Itinerary, r *award.Result) { r.Query.Adults = 1 },
		"cabin query":            func(_ *award.Query, _ *award.Itinerary, r *award.Result) { r.Query.Cabin = award.Business },
		"zero party":             func(q *award.Query, _ *award.Itinerary, r *award.Result) { q.Adults, r.Query.Adults = 0, 0 },
		"unsupported party":      func(q *award.Query, _ *award.Itinerary, r *award.Result) { q.Adults, r.Query.Adults = 7, 7 },
		"empty target":           func(_ *award.Query, i *award.Itinerary, _ *award.Result) { i.Segments = nil },
		"missing cash itinerary": func(_ *award.Query, _ *award.Itinerary, r *award.Result) { r.Itineraries = nil },
		"different flight": func(_ *award.Query, _ *award.Itinerary, r *award.Result) {
			r.Itineraries[0].Segments[0].FlightNumber = "BR2"
		},
		"invalid matching flight": func(_ *award.Query, i *award.Itinerary, r *award.Result) {
			i.Segments[0].FlightNumber = "other"
			r.Itineraries[0].Segments[0].FlightNumber = "other"
		},
		"segment origin": func(_ *award.Query, _ *award.Itinerary, r *award.Result) { r.Itineraries[0].Segments[0].Origin = "TSA" },
		"segment destination": func(_ *award.Query, _ *award.Itinerary, r *award.Result) {
			r.Itineraries[0].Segments[0].Destination = "MFM"
		},
		"departure": func(_ *award.Query, _ *award.Itinerary, r *award.Result) {
			r.Itineraries[0].Segments[0].DepartureLocal = "2026-11-27T09:00:00"
		},
		"arrival": func(_ *award.Query, _ *award.Itinerary, r *award.Result) {
			r.Itineraries[0].Segments[0].ArrivalLocal = "2026-11-27T11:00:00"
		},
		"empty matching times": func(_ *award.Query, i *award.Itinerary, r *award.Result) {
			i.Segments[0].ArrivalLocal = ""
			r.Itineraries[0].Segments[0].ArrivalLocal = ""
		},
		"target outside requested day": func(_ *award.Query, i *award.Itinerary, r *award.Result) {
			i.Segments[0].DepartureLocal = "2026-11-28T08:00:00"
			r.Itineraries[0].Segments[0].DepartureLocal = i.Segments[0].DepartureLocal
		},
		"target outside requested route": func(_ *award.Query, i *award.Itinerary, r *award.Result) {
			i.Segments[0].Origin = "TSA"
			r.Itineraries[0].Segments[0].Origin = "TSA"
		},
		"different operator": func(_ *award.Query, _ *award.Itinerary, r *award.Result) {
			r.Itineraries[0].Segments[0].OperatingAirline = "UNI Air"
		},
		"cash actual cabin mismatch": func(_ *award.Query, _ *award.Itinerary, r *award.Result) {
			r.Itineraries[0].Segments[0].Cabin = award.Business
		},
		"unknown target cabin":   func(_ *award.Query, i *award.Itinerary, _ *award.Result) { i.Segments[0].Cabin = "" },
		"different target cabin": func(_ *award.Query, i *award.Itinerary, _ *award.Result) { i.Segments[0].Cabin = award.Business },
		"ambiguous itinerary": func(_ *award.Query, _ *award.Itinerary, r *award.Result) {
			r.Itineraries = append(r.Itineraries, r.Itineraries[0])
		},
		"ambiguous itinerary without fare": func(_ *award.Query, _ *award.Itinerary, r *award.Result) {
			duplicate := r.Itineraries[0]
			duplicate.Fares = nil
			r.Itineraries = append(r.Itineraries, duplicate)
		},
	} {
		t.Run(name, func(t *testing.T) {
			q, target, cash := cashTaxMatchFixture()
			change(&q, &target, &cash)
			if _, _, ok := cashTaxCandidate(q, target, cash); ok {
				t.Fatal("mismatched or ambiguous cash candidate accepted")
			}
		})
	}
}

func TestCashTaxCandidateRequiresUsableCashFareAndKeepsStableFamily(t *testing.T) {
	for name, change := range map[string]func(*award.Fare){
		"different cabin":    func(f *award.Fare) { f.Cabin = award.Business },
		"missing cabin":      func(f *award.Fare) { f.Cabin = "" },
		"missing quote ID":   func(f *award.Fare) { f.QuoteID = " " },
		"missing cash price": func(f *award.Fare) { f.CashPrice = nil },
		"missing currency":   func(f *award.Fare) { f.CashPrice.Currency = "" },
		"invalid currency":   func(f *award.Fare) { f.CashPrice.Currency = "123" },
		"lowercase currency": func(f *award.Fare) { f.CashPrice.Currency = "twd" },
		"invalid amount":     func(f *award.Fare) { f.CashPrice.Amount = "1/2" },
		"negative amount":    func(f *award.Fare) { f.CashPrice.Amount = "-1" },
		"zero amount":        func(f *award.Fare) { f.CashPrice.Amount = "0" },
	} {
		t.Run(name, func(t *testing.T) {
			q, target, cash := cashTaxMatchFixture()
			change(&cash.Itineraries[0].Fares[0])
			if _, _, ok := cashTaxCandidate(q, target, cash); ok {
				t.Fatal("invalid cash fare accepted")
			}
		})
	}
	q, target, cash := cashTaxMatchFixture()
	first := cash.Itineraries[0].Fares[0]
	cheaper := first
	cheaper.Name, cheaper.QuoteID, cheaper.CashPrice = "Economy Discount", "other-quote", &award.Money{Amount: "50", Currency: "TWD"}
	invalid := first
	invalid.QuoteID = ""
	cash.Itineraries[0].Fares = []award.Fare{invalid, first, cheaper}
	_, fare, ok := cashTaxCandidate(q, target, cash)
	if !ok || fare.QuoteID != first.QuoteID {
		t.Fatal("first valid stable family was not selected")
	}
}

func TestCashTaxCandidateMultiLegAndMixedCabinBoundaries(t *testing.T) {
	q, target, cash := cashTaxMatchFixture()
	q.Origin, cash.Query.Origin = "KHH", "KHH"
	connection := award.Segment{Cabin: award.Economy, FlightNumber: "B7002", Airline: "UNI Air", Origin: "KHH", Destination: "TPE", DepartureLocal: q.Date + "T05:00:00", ArrivalLocal: q.Date + "T06:00:00"}
	target.Segments = append([]award.Segment{connection}, target.Segments...)
	connection.Cabin, connection.FlightNumber = "", "B7 2"
	cash.Itineraries[0].Segments = append([]award.Segment{connection}, cash.Itineraries[0].Segments...)
	if _, _, ok := cashTaxCandidate(q, target, cash); !ok {
		t.Fatal("homogeneous multi-leg award did not match")
	}
	target.Segments[1].Cabin = award.Business
	if _, _, ok := cashTaxCandidate(q, target, cash); ok {
		t.Fatal("mixed-cabin award received a cash-derived estimate")
	}
	target.Segments[1].Cabin = award.Economy
	cash.Itineraries[0].Segments[0], cash.Itineraries[0].Segments[1] = cash.Itineraries[0].Segments[1], cash.Itineraries[0].Segments[0]
	if _, _, ok := cashTaxCandidate(q, target, cash); ok {
		t.Fatal("reordered segments matched")
	}
}

func TestCashTaxCandidateOperatorOnlyComparedWhenBothKnown(t *testing.T) {
	q, target, cash := cashTaxMatchFixture()
	target.Segments[0].Airline = ""
	if _, _, ok := cashTaxCandidate(q, target, cash); !ok {
		t.Fatal("missing operator information incorrectly rejected exact flights")
	}
	target.Segments[0].OperatingAirline = "UNI Air"
	if _, _, ok := cashTaxCandidate(q, target, cash); ok {
		t.Fatal("explicit operating airline mismatch ignored")
	}
}

func cashTaxSelectedViewFixture() (award.Query, award.Itinerary, map[string]any) {
	q, target, _ := cashTaxMatchFixture()
	segment := map[string]any{
		"beginLocation": map[string]any{"locationCode": "TPE"}, "endLocation": map[string]any{"locationCode": "HKG"},
		"beginDate": "Nov 27, 2026, 8:00:00 AM", "endDate": "Nov 27, 2026, 10:00:00 AM",
		"airline": map[string]any{"code": "BR", "name": "EVA Air"}, "opAirline": map[string]any{"name": "EVA Airways"}, "flightNumber": "1",
		"listCabin": []any{map[string]any{"name": "Economy Class", "code": "E", "isWaitlisted": false}},
	}
	item := map[string]any{"fareFamily": map[string]any{"ffName": "Economy<br>Basic"}, "listSegment": []any{segment}}
	return q, target, map[string]any{"listItineraryElem": []any{item}}
}

func TestValidateCashTaxSelectionUsesActualFAREItineraryShape(t *testing.T) {
	q, target, view := cashTaxSelectedViewFixture()
	raw, _ := json.Marshal(view)
	if err := validateCashTaxSelection(raw, target, q); err != nil {
		t.Fatalf("captured selected-itinerary shape rejected: %v", err)
	}
}

func TestValidateCashTaxSelectionRejectsMissingOrMismatchedEvidence(t *testing.T) {
	for name, change := range map[string]func(*award.Query, *award.Itinerary, map[string]any, map[string]any, map[string]any){
		"missing itinerary": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) {
			delete(view, "listItineraryElem")
		},
		"ambiguous itinerary": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) {
			view["listItineraryElem"] = []any{item, item}
		},
		"missing segments": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) {
			delete(item, "listSegment")
		},
		"flight": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) {
			segment["flightNumber"] = "2"
		},
		"origin": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) {
			segment["beginLocation"] = map[string]any{"locationCode": "TSA"}
		},
		"destination": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) {
			segment["endLocation"] = map[string]any{"locationCode": "MFM"}
		},
		"departure": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) {
			segment["beginDate"] = "2026-11-27T09:00:00"
		},
		"arrival": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) {
			segment["endDate"] = "2026-11-27T11:00:00"
		},
		"invalid time": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) {
			segment["endDate"] = "not-a-date"
		},
		"operator": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) {
			segment["opAirline"] = map[string]any{"name": "UNI Air"}
		},
		"operator code without name": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) {
			segment["opAirline"] = map[string]any{"code": "B7"}
		},
		"family cabin": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) {
			item["fareFamily"] = map[string]any{"ffName": "Business Basic"}
		},
		"actual cabin": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) {
			segment["listCabin"] = []any{map[string]any{"name": "Business Class", "code": "C"}}
		},
		"missing cabin": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) {
			delete(segment, "listCabin")
		},
		"waitlisted cabin": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) {
			segment["listCabin"] = []any{map[string]any{"name": "Economy Class", "isWaitlisted": true}}
		},
		"deleted": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) { item["isDeleted"] = true },
		"flown":   func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) { item["flown"] = true },
		"no show": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) { item["noShow"] = true },
		"waitlisted itinerary": func(_ *award.Query, _ *award.Itinerary, view, item, segment map[string]any) {
			item["isWaitlisted"] = true
		},
		"different query": func(q *award.Query, _ *award.Itinerary, view, item, segment map[string]any) { q.Date = "2026-11-28" },
		"different target cabin": func(_ *award.Query, target *award.Itinerary, view, item, segment map[string]any) {
			target.Segments[0].Cabin = award.Business
		},
	} {
		t.Run(name, func(t *testing.T) {
			q, target, view := cashTaxSelectedViewFixture()
			item := view["listItineraryElem"].([]any)[0].(map[string]any)
			segment := item["listSegment"].([]any)[0].(map[string]any)
			change(&q, &target, view, item, segment)
			raw, _ := json.Marshal(view)
			if validateCashTaxSelection(raw, target, q) == nil {
				t.Fatal("unconfirmed selected itinerary accepted")
			}
		})
	}
	q, target, _ := cashTaxSelectedViewFixture()
	for _, raw := range []string{"", "null", "{}", "[]", "{broken"} {
		if validateCashTaxSelection(json.RawMessage(raw), target, q) == nil {
			t.Fatal("invalid selection accepted")
		}
	}
}

func TestValidateCashTaxSelectionMatchesEveryOrderedConnection(t *testing.T) {
	q, target, view := cashTaxSelectedViewFixture()
	q.Origin = "KHH"
	connection := award.Segment{Cabin: award.Economy, FlightNumber: "B7002", Airline: "UNI Air", Origin: "KHH", Destination: "TPE", DepartureLocal: q.Date + "T05:00:00", ArrivalLocal: q.Date + "T06:00:00"}
	target.Segments = append([]award.Segment{connection}, target.Segments...)
	item := view["listItineraryElem"].([]any)[0].(map[string]any)
	connectionJSON := map[string]any{
		"beginLocation": map[string]any{"locationCode": "KHH"}, "endLocation": map[string]any{"locationCode": "TPE"},
		"beginDate": connection.DepartureLocal, "endDate": connection.ArrivalLocal,
		"airline": map[string]any{"code": "B7", "name": "UNI Air"}, "flightNumber": "2",
		"listCabin": []any{map[string]any{"name": "Economy Class", "code": "E"}},
	}
	segments := append([]any{connectionJSON}, item["listSegment"].([]any)...)
	item["listSegment"] = segments
	raw, _ := json.Marshal(view)
	if err := validateCashTaxSelection(raw, target, q); err != nil {
		t.Fatalf("exact selected connection rejected: %v", err)
	}
	segments[0], segments[1] = segments[1], segments[0]
	raw, _ = json.Marshal(view)
	if validateCashTaxSelection(raw, target, q) == nil {
		t.Fatal("selected connection order ignored")
	}
	item["listSegment"] = segments[:1]
	raw, _ = json.Marshal(view)
	if validateCashTaxSelection(raw, target, q) == nil {
		t.Fatal("missing selected connection accepted")
	}
}

func TestCashTaxMatchingSupportsEachHomogeneousRequestedCabin(t *testing.T) {
	for _, tt := range []struct {
		cabin award.Cabin
		label string
	}{{award.Economy, "Economy Class"}, {award.PremiumEconomy, "Premium Economy Class"}, {award.Business, "Royal Laurel Class"}} {
		t.Run(string(tt.cabin), func(t *testing.T) {
			q, target, cash := cashTaxMatchFixture()
			q.Cabin, target.Segments[0].Cabin, cash.Query.Cabin, cash.Itineraries[0].Fares[0].Cabin = tt.cabin, tt.cabin, tt.cabin, tt.cabin
			if _, _, ok := cashTaxCandidate(q, target, cash); !ok {
				t.Fatal("matching requested cash fare cabin rejected")
			}
			_, _, view := cashTaxSelectedViewFixture()
			item := view["listItineraryElem"].([]any)[0].(map[string]any)
			item["fareFamily"] = map[string]any{"ffName": tt.label}
			segment := item["listSegment"].([]any)[0].(map[string]any)
			segment["listCabin"] = []any{map[string]any{"name": tt.label}}
			raw, _ := json.Marshal(view)
			if err := validateCashTaxSelection(raw, target, q); err != nil {
				t.Fatalf("matching selected cabin rejected: %v", err)
			}
		})
	}
}
