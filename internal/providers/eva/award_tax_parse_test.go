package eva

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func cashTaxFixture(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/award-cash-tax-price.json")
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

func cashTaxGroup(p map[string]any) map[string]any {
	return p["listTravellerPrice"].([]any)[0].(map[string]any)
}

func cashTaxSections(p map[string]any) []any {
	return cashTaxGroup(p)["travellerPrice"].([]any)[0].(map[string]any)["priceBean"].(map[string]any)["recoAmount"].(map[string]any)["listTaxes"].([]any)
}

func TestParseCashTaxEstimateSanitizedPriceAndSingleBreakdownSource(t *testing.T) {
	p := cashTaxFixture(t)

	group := cashTaxGroup(p)
	group["travellerTypePrice"] = group["travellerPrice"]
	group["travellerPrice"].([]any)[0].(map[string]any)["taxes"] = cashTaxSections(p)
	raw, _ := json.Marshal(p)
	when := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	e, err := parseCashTaxEstimate(raw, 1, when)
	if err != nil {
		t.Fatal(err)
	}
	if e.Total.Amount != "3047" || e.Total.Currency != "TWD" || e.Source != "eva_cash_fare" || !e.QuotedAt.Equal(when) || len(e.Components) != 3 {
		t.Fatalf("wrong estimated cash taxes: %+v", e)
	}
	for i, code := range []string{"YQAC", "YRVB", "TWAE"} {
		if e.Components[i].Code != code || e.Components[i].Amount.Currency != "TWD" {
			t.Fatalf("wrong component: %+v", e.Components[i])
		}
	}
}

func TestParseCashTaxEstimateExactPartyDecimals(t *testing.T) {
	p := cashTaxFixture(t)
	p["totalTaxes"].(map[string]any)["amount"] = json.Number("1.8")
	p["totalTaxesPerPax"].(map[string]any)["amount"] = json.Number("0.6")
	p["totalAmount"].(map[string]any)["amount"] = json.Number("1.8000000000000000000000000000000001")
	cashTaxGroup(p)["traveller"].(map[string]any)["travellersNumber"] = 3
	sections := cashTaxSections(p)
	sections[0].([]any)[1].(map[string]any)["value"] = json.Number("1e-1")
	sections[0].([]any)[2].(map[string]any)["value"] = json.Number("0.2")
	sections[1].([]any)[0].(map[string]any)["value"] = json.Number("0.3")
	raw, _ := json.Marshal(p)
	e, err := parseCashTaxEstimate(raw, 3, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !equalCashAmounts(e.Total.Amount, "1.8") {
		t.Fatalf("wrong party total: %+v", e)
	}
	for i, want := range []string{"0.3", "0.6", "0.9"} {
		if !equalCashAmounts(e.Components[i].Amount.Amount, want) {
			t.Fatalf("component not multiplied exactly: %+v", e.Components[i])
		}
	}
}

func TestParseCashTaxEstimateAcceptsTaxesEqualToCashTotal(t *testing.T) {
	p := cashTaxFixture(t)
	p["totalAmount"].(map[string]any)["amount"] = json.Number("3047.00")
	raw, _ := json.Marshal(p)
	if e, err := parseCashTaxEstimate(raw, 1, time.Now()); err != nil || e == nil {
		t.Fatalf("equal cash and tax totals rejected: %+v %v", e, err)
	}
}

func TestParseCashTaxEstimateRejectsInconsistentOrAmbiguousData(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"cash offset mode", func(p map[string]any) { p["isMilesMode"] = true }},
		{"missing cash mode", func(p map[string]any) { delete(p, "isMilesMode") }},
		{"missing currency", func(p map[string]any) { delete(p, "currency") }},
		{"invalid currency", func(p map[string]any) { p["currency"].(map[string]any)["code"] = "TW" }},
		{"total currency mismatch", func(p map[string]any) { p["totalAmount"].(map[string]any)["currency"].(map[string]any)["code"] = "USD" }},
		{"missing total", func(p map[string]any) { delete(p, "totalAmount") }},
		{"missing total amount", func(p map[string]any) { delete(p["totalAmount"].(map[string]any), "amount") }},
		{"null total amount", func(p map[string]any) { p["totalAmount"].(map[string]any)["amount"] = nil }},
		{"negative cash total", func(p map[string]any) { p["totalAmount"].(map[string]any)["amount"] = -1 }},
		{"malformed cash total", func(p map[string]any) { p["totalAmount"].(map[string]any)["amount"] = "not a number" }},
		{"percentage cash total", func(p map[string]any) { p["totalAmount"].(map[string]any)["isPercentage"] = true }},
		{"cash total below taxes", func(p map[string]any) { p["totalAmount"].(map[string]any)["amount"] = 3046 }},
		{"cash total below taxes exact decimal", func(p map[string]any) {
			p["totalAmount"].(map[string]any)["amount"] = json.Number("3046.9999999999999999999999999999999999")
		}},
		{"tax currency mismatch", func(p map[string]any) { p["totalTaxes"].(map[string]any)["currency"] = map[string]any{"code": "USD"} }},
		{"percentage taxes", func(p map[string]any) { p["totalTaxes"].(map[string]any)["isPercentage"] = true }},
		{"negative total", func(p map[string]any) { p["totalTaxes"].(map[string]any)["amount"] = -1 }},
		{"missing per pax", func(p map[string]any) { delete(p, "totalTaxesPerPax") }},
		{"null total", func(p map[string]any) { p["totalTaxes"].(map[string]any)["amount"] = nil }},
		{"party count", func(p map[string]any) { cashTaxGroup(p)["traveller"].(map[string]any)["travellersNumber"] = 2 }},
		{"non adult", func(p map[string]any) { cashTaxGroup(p)["traveller"].(map[string]any)["travellerCode"] = "CHD" }},
		{"multiple groups", func(p map[string]any) {
			p["listTravellerPrice"] = append(p["listTravellerPrice"].([]any), cashTaxGroup(p))
		}},
		{"multiple traveller prices", func(p map[string]any) {
			g := cashTaxGroup(p)
			g["travellerPrice"] = append(g["travellerPrice"].([]any), g["travellerPrice"].([]any)[0])
		}},
		{"no breakdown", func(p map[string]any) {
			cashTaxGroup(p)["travellerPrice"].([]any)[0].(map[string]any)["priceBean"] = nil
		}},
		{"component sum mismatch", func(p map[string]any) { cashTaxSections(p)[0].([]any)[1].(map[string]any)["value"] = 1223 }},
		{"negative component", func(p map[string]any) { cashTaxSections(p)[0].([]any)[1].(map[string]any)["value"] = -1222 }},
		{"component currency", func(p map[string]any) {
			cashTaxSections(p)[0].([]any)[1].(map[string]any)["currency"].(map[string]any)["code"] = "USD"
		}},
		{"missing component code", func(p map[string]any) { delete(cashTaxSections(p)[0].([]any)[1].(map[string]any), "code") }},
		{"duplicate component code", func(p map[string]any) { cashTaxSections(p)[0].([]any)[2].(map[string]any)["code"] = "YQAC" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := cashTaxFixture(t)
			tt.mutate(p)
			raw, _ := json.Marshal(p)
			if e, err := parseCashTaxEstimate(raw, 1, time.Now()); err == nil || e != nil {
				t.Fatalf("invalid estimate accepted: %+v %v", e, err)
			}
		})
	}
	raw, _ := json.Marshal(cashTaxFixture(t))
	for _, adults := range []int{0, 7} {
		if e, err := parseCashTaxEstimate(raw, adults, time.Now()); err == nil || e != nil {
			t.Fatal("invalid party accepted")
		}
	}
	if e, err := parseCashTaxEstimate(raw, 1, time.Time{}); err == nil || e != nil {
		t.Fatal("missing quote timestamp accepted")
	}
	if e, err := parseCashTaxEstimate([]byte(`not json`), 1, time.Now()); err == nil || e != nil {
		t.Fatal("malformed quote accepted")
	}
}
