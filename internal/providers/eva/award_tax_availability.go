package eva

import (
	"encoding/json"
	"math/big"
	"strconv"
	"time"

	"awarddesk/internal/award"
)

type cashAvailabilityAmount struct {
	TotalAmount, AmountWithoutTax, AmountWithoutTaxAndFee, Tax, ServiceFee json.Number
	HasDynamicDiscount                                                     *bool
}

func validCashAvailabilityAmount(a cashAvailabilityAmount) bool {
	if a.HasDynamicDiscount == nil || *a.HasDynamicDiscount {
		return false
	}
	for _, number := range []json.Number{a.TotalAmount, a.AmountWithoutTax, a.AmountWithoutTaxAndFee, a.Tax, a.ServiceFee} {
		if _, err := partyCashAmount(number.String(), 1); err != nil {
			return false
		}
	}
	if !equalCashAmounts(a.ServiceFee.String(), "0") || !equalCashAmounts(a.AmountWithoutTax.String(), a.AmountWithoutTaxAndFee.String()) {
		return false
	}
	base, _ := new(big.Rat).SetString(a.AmountWithoutTax.String())
	tax, _ := new(big.Rat).SetString(a.Tax.String())
	total, _ := new(big.Rat).SetString(a.TotalAmount.String())
	return new(big.Rat).Add(base, tax).Cmp(total) == 0
}

func cashAvailabilityTaxEstimate(page pageData, flight award.Itinerary, fare award.Fare, adults int, quotedAt time.Time) (*award.TaxEstimate, bool) {
	var mode struct{ IsMilesMode *bool }
	var a struct {
		CurrencyBean       namedCode
		FareFamilyList     []struct{ FFCode, FFName string }
		RecommendationList []struct {
			RecoID     *int
			FFCode     string
			RecoAmount cashAvailabilityAmount
			Bounds     []struct {
				BoundAmount     cashAvailabilityAmount
				TravellerPrices map[string]json.Number
				FlightGroupList []struct{ FlightID *int }
			}
		}
	}
	if adults < 1 || adults > 6 || quotedAt.IsZero() || fare.CashPrice == nil || json.Unmarshal(page.Business["Price"], &mode) != nil || mode.IsMilesMode == nil || *mode.IsMilesMode || json.Unmarshal(page.Business["Availability"], &a) != nil || !cashTaxCurrency.MatchString(a.CurrencyBean.Code) || a.CurrencyBean.Code != fare.CashPrice.Currency {
		return nil, false
	}
	family := ""
	for _, value := range a.FareFamilyList {
		if plainName(value.FFName) == fare.Name {
			if family != "" {
				return nil, false
			}
			family = value.FFCode
		}
	}
	if family == "" {
		return nil, false
	}
	var estimate *award.TaxEstimate
	for _, rec := range a.RecommendationList {
		if rec.RecoID == nil || strconv.Itoa(*rec.RecoID) != fare.QuoteID {
			continue
		}
		if estimate != nil || rec.FFCode != family || len(rec.Bounds) != 1 || !validCashAvailabilityAmount(rec.RecoAmount) {
			return nil, false
		}
		bound := rec.Bounds[0]
		if !validCashAvailabilityAmount(bound.BoundAmount) || !equalCashAmounts(bound.BoundAmount.TotalAmount.String(), rec.RecoAmount.TotalAmount.String()) || !equalCashAmounts(bound.BoundAmount.Tax.String(), rec.RecoAmount.Tax.String()) || len(bound.TravellerPrices) != 1 || !equalCashAmounts(bound.TravellerPrices["ADT"].String(), rec.RecoAmount.TotalAmount.String()) {
			return nil, false
		}
		matched := 0
		for _, group := range bound.FlightGroupList {
			if group.FlightID != nil && strconv.Itoa(*group.FlightID) == flight.ID {
				matched++
			}
		}
		partyPrice, err := partyCashAmount(rec.RecoAmount.TotalAmount.String(), adults)
		if err != nil || matched != 1 || !equalCashAmounts(partyPrice, fare.CashPrice.Amount) {
			return nil, false
		}
		total, err := partyCashAmount(rec.RecoAmount.Tax.String(), adults)
		if err != nil {
			return nil, false
		}
		estimate = &award.TaxEstimate{Total: award.Money{Amount: total, Currency: a.CurrencyBean.Code}, Source: "eva_cash_availability", QuotedAt: quotedAt}
	}
	return estimate, estimate != nil
}
