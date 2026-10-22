package eva

import (
	"context"
	"testing"

	"awarddesk/internal/award"
)

func TestPreservedQuotesRequireUnchangedFlightAndFare(t *testing.T) {
	old := award.Result{Itineraries: []award.Itinerary{{
		ID: "old",
		Segments: []award.Segment{{
			FlightNumber: "BR1", Origin: "TPE", Destination: "HKG",
			DepartureLocal: "2026-11-24T10:00:00",
		}},
		Fares: []award.Fare{{
			QuoteID: "1", Name: "Basic", Cabin: award.Economy,
			CashPrice: &award.Money{Amount: "100", Currency: "TWD"},
			MilesCash: &award.MilesCash{Miles: 1000}, Estimated: true,
		}},
	}}}
	for _, field := range []string{"unchanged", "flight", "departure", "connection", "family", "cabin", "amount", "currency", "ineligible", "missing price"} {
		t.Run(field, func(t *testing.T) {
			fresh := award.CloneResult(old)
			item := &fresh.Itineraries[0]
			item.ID = "new"
			fare := &item.Fares[0]
			fare.QuoteID, fare.MilesCash, fare.Estimated = "2", nil, false
			switch field {
			case "flight":
				item.Segments[0].FlightNumber = "BR2"
			case "departure":
				item.Segments[0].DepartureLocal = "2026-11-24T11:00:00"
			case "connection":
				item.Segments = append(item.Segments, item.Segments[0])
			case "family":
				fare.Name = "Up"
			case "cabin":
				fare.Cabin = award.Business
			case "amount":
				fare.CashPrice.Amount = "101"
			case "currency":
				fare.CashPrice.Currency = "USD"
			case "ineligible":
				eligible := false
				fare.MilesCashEligible = &eligible
			case "missing price":
				fare.CashPrice = nil
			}
			preserveMatchingQuotes(&fresh, old)
			if (fare.MilesCash != nil) != (field == "unchanged") {
				t.Fatal("wrong quote reused")
			}
			if item.ID != "new" || fare.QuoteID != "2" {
				t.Fatal("stale identifiers copied")
			}
			if fare.MilesCash != nil {
				fare.MilesCash.Miles = 999
				if old.Itineraries[0].Fares[0].MilesCash.Miles != 1000 {
					t.Fatal("shared mutable quote")
				}
			}
		})
	}
}

func TestPricingPlanSkipsDuplicateFinishedAndIneligibleFares(t *testing.T) {
	no := false
	r := award.Result{Itineraries: []award.Itinerary{
		{Fares: []award.Fare{
			{QuoteID: "1"}, {QuoteID: "1"},
			{QuoteID: "2", MilesCash: &award.MilesCash{}},
			{QuoteID: "3", MilesCashEligible: &no},
		}},
		{Fares: []award.Fare{{QuoteID: "1"}, {QuoteID: "4"}}},
	}}
	plan := unpricedFares(r)
	if len(plan) != 2 || plan[0].fare.QuoteID != "1" || plan[1].fare.QuoteID != "4" {
		t.Fatal("wrong pricing work selected")
	}
}

func TestMissingCashPriceStopsBeforeAnyPricingRequest(t *testing.T) {
	r := award.Result{Itineraries: []award.Itinerary{{Fares: []award.Fare{{QuoteID: "1"}}}}}
	// A nil client would panic if a request were attempted.
	err := priceFaresWithReplans(context.Background(), nil, pageData{}, &r, nil, 2)
	if err == nil {
		t.Fatal("missing price accepted")
	}
}
