package eva

import (
	"encoding/json"

	"awarddesk/internal/award"
)

func flightKey(segments []award.Segment) string {
	fields := make([][4]string, len(segments))
	for i, s := range segments {
		fields[i] = [4]string{s.FlightNumber, s.Origin, s.Destination, s.DepartureLocal}
	}
	data, _ := json.Marshal(fields)
	return string(data)
}

type quoteKey struct {
	flight, family, amount, currency string
	cabin                            award.Cabin
}

func fareKey(flight string, fare award.Fare) (quoteKey, bool) {
	if fare.CashPrice == nil {
		return quoteKey{}, false
	}
	return quoteKey{flight: flight, family: fare.Name, cabin: fare.Cabin,
		amount: fare.CashPrice.Amount, currency: fare.CashPrice.Currency}, true
}

func matchingSelection(fresh award.Result, wanted award.Itinerary, fare award.Fare) (string, string, error) {
	wantedKey, valid := fareKey(flightKey(wanted.Segments), fare)
	if valid {
		for _, item := range fresh.Itineraries {
			flight := flightKey(item.Segments)
			for _, candidate := range item.Fares {
				key, ok := fareKey(flight, candidate)
				if ok && key == wantedKey {
					return item.ID, candidate.QuoteID, nil
				}
			}
		}
	}
	return "", "", award.Failure("price_changed", "EVA changed flight availability or fare prices during pricing; run a fresh search")
}

func preserveMatchingQuotes(fresh *award.Result, previous award.Result) {
	quotes := make(map[quoteKey]award.Fare)
	for _, item := range previous.Itineraries {
		flight := flightKey(item.Segments)
		for _, fare := range item.Fares {
			if key, ok := fareKey(flight, fare); ok && fare.MilesCash != nil {
				quotes[key] = fare
			}
		}
	}
	for x := range fresh.Itineraries {
		item := &fresh.Itineraries[x]
		flight := flightKey(item.Segments)
		for y := range item.Fares {
			fare := &item.Fares[y]
			if fare.MilesCashEligible != nil && !*fare.MilesCashEligible {
				continue
			}
			key, valid := fareKey(flight, *fare)
			if old, ok := quotes[key]; valid && ok {
				copy := *old.MilesCash
				fare.MilesCash, fare.Estimated = &copy, old.Estimated
			}
		}
	}
}
