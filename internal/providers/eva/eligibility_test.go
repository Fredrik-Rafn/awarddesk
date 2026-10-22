package eva

import (
	"awarddesk/internal/award"
	"testing"
)

func TestMilesEligibilityRemovesFlightsAndFareOptionsWithoutMutatingSource(t *testing.T) {
	yes, no := true, false
	original := award.Result{Itineraries: []award.Itinerary{
		{ID: "mixed", Fares: []award.Fare{{Name: "cash only", MilesCashEligible: &no}, {Name: "miles", MilesCashEligible: &yes}}},
		{ID: "cash-only", Fares: []award.Fare{{MilesCashEligible: &no}}},
		{ID: "legacy", Fares: []award.Fare{{Name: "unknown legacy eligibility"}}},
	}}
	filtered := milesEligibleResult(original)
	if len(filtered.Itineraries) != 2 || filtered.Itineraries[0].ID != "mixed" || len(filtered.Itineraries[0].Fares) != 1 || filtered.Itineraries[0].Fares[0].Name != "miles" {
		t.Fatal("ineligible options leaked")
	}
	if len(original.Itineraries) != 3 || len(original.Itineraries[0].Fares) != 2 {
		t.Fatal("filter mutated provider source")
	}
}
