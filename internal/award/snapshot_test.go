package award

import "testing"

func TestCloneResultIsolatesTaxEstimateAndComponents(t *testing.T) {
	r := Result{Itineraries: []Itinerary{{Fares: []Fare{{Points: 10000, TaxesUSD: &USDConversion{Money: Money{Amount: "2", Currency: "USD"}}, TaxEstimate: &TaxEstimate{
		Total:      Money{Amount: "3047", Currency: "TWD"},
		USD:        &USDConversion{Money: Money{Amount: "93.49", Currency: "USD"}},
		Components: []TaxComponent{{Code: "TWAE", Amount: Money{Amount: "750", Currency: "TWD"}}},
	}}}}}}
	copy := CloneResult(r)
	copy.Itineraries[0].Fares[0].TaxEstimate.Total.Amount = "0"
	copy.Itineraries[0].Fares[0].TaxEstimate.Components[0].Amount.Amount = "0"
	copy.Itineraries[0].Fares[0].TaxEstimate.USD.Amount = "0"
	copy.Itineraries[0].Fares[0].TaxesUSD.Amount = "0"
	original := r.Itineraries[0].Fares[0]
	if original.TaxEstimate.Total.Amount != "3047" || original.TaxEstimate.Components[0].Amount.Amount != "750" || original.TaxEstimate.USD.Amount != "93.49" || original.TaxesUSD.Amount != "2" || original.Points != 10000 {
		t.Fatal("a preview changed a retained award quote")
	}
}
