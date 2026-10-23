package eva

import (
	"encoding/json"
	"math/big"
	"regexp"
	"strings"
	"time"

	"awarddesk/internal/award"
)

var cashTaxCurrency = regexp.MustCompile(`^[A-Z]{3}$`)

func parseCashTaxEstimate(raw json.RawMessage, adults int, quotedAt time.Time) (*award.TaxEstimate, error) {
	type amount struct {
		Amount       json.Number
		Currency     namedCode
		IsPercentage bool
	}
	type tax struct {
		Code, Name string
		Value      json.Number
		Currency   namedCode
		IsSubTotal bool
	}
	var p struct {
		Currency                                  namedCode
		IsMilesMode                               *bool
		TotalAmount, TotalTaxes, TotalTaxesPerPax amount
		ListTravellerPrice                        []struct {
			Traveller struct {
				TravellerCode    string
				TravellersNumber int
			}
			TravellerPrice []struct {
				PriceBean struct {
					RecoAmount struct{ ListTaxes [][]tax }
				}
			}
		}
	}
	fail := func() (*award.TaxEstimate, error) {
		return nil, award.Failure("tax_estimate_protocol", "EVA cash fare tax breakdown could not be verified")
	}
	if adults < 1 || adults > 6 || quotedAt.IsZero() || json.Unmarshal(raw, &p) != nil || p.IsMilesMode == nil || *p.IsMilesMode || !cashTaxCurrency.MatchString(p.Currency.Code) || p.TotalAmount.Currency.Code != p.Currency.Code || len(p.ListTravellerPrice) != 1 {
		return fail()
	}
	currency := p.Currency.Code
	if p.TotalAmount.IsPercentage {
		return fail()
	}
	if _, err := partyCashAmount(p.TotalAmount.Amount.String(), 1); err != nil {
		return fail()
	}
	for _, a := range []amount{p.TotalTaxes, p.TotalTaxesPerPax} {
		if a.IsPercentage || (a.Currency.Code != "" && a.Currency.Code != currency) {
			return fail()
		}
		if _, err := partyCashAmount(a.Amount.String(), 1); err != nil {
			return fail()
		}
	}
	group := p.ListTravellerPrice[0]
	if group.Traveller.TravellerCode != "ADT" || group.Traveller.TravellersNumber != adults || len(group.TravellerPrice) != 1 {
		return fail()
	}
	total, err := partyCashAmount(p.TotalTaxesPerPax.Amount.String(), adults)
	if err != nil || !equalCashAmounts(total, p.TotalTaxes.Amount.String()) {
		return fail()
	}
	cashTotal, cashOK := new(big.Rat).SetString(p.TotalAmount.Amount.String())
	taxTotal, taxOK := new(big.Rat).SetString(total)
	if !cashOK || !taxOK || taxTotal.Cmp(cashTotal) > 0 {
		return fail()
	}
	estimate := &award.TaxEstimate{Total: award.Money{Amount: total, Currency: currency}, Source: "eva_cash_fare", QuotedAt: quotedAt}
	sum := new(big.Rat)
	seen := map[string]bool{}
	for _, section := range group.TravellerPrice[0].PriceBean.RecoAmount.ListTaxes {
		for _, row := range section {
			code, name := strings.TrimSpace(row.Code), strings.TrimSpace(row.Name)
			if row.IsSubTotal || strings.EqualFold(code, "FARE_BASIS") || strings.EqualFold(name, "Fare") || strings.EqualFold(name, "Base fare") {
				continue
			}
			if code == "" || name == "" || seen[code] || row.Currency.Code != currency {
				return fail()
			}
			party, err := partyCashAmount(row.Value.String(), adults)
			if err != nil {
				return fail()
			}
			value, ok := new(big.Rat).SetString(row.Value.String())
			if !ok {
				return fail()
			}
			sum.Add(sum, value)
			seen[code] = true
			estimate.Components = append(estimate.Components, award.TaxComponent{Code: code, Name: name, Amount: award.Money{Amount: party, Currency: currency}})
		}
	}
	perPax, ok := new(big.Rat).SetString(p.TotalTaxesPerPax.Amount.String())
	if !ok || len(estimate.Components) == 0 || sum.Cmp(perPax) != 0 {
		return fail()
	}
	return estimate, nil
}
