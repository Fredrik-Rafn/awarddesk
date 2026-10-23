package eva

import (
	"encoding/json"
	"math/big"
	"strings"
	"time"

	"awarddesk/internal/award"
)

func cashTaxCandidate(q award.Query, target award.Itinerary, cash award.Result) (award.Itinerary, award.Fare, bool) {
	if q.Provider != "eva" || q.Mode != "award" || q.Adults < 1 || q.Adults > 6 || (q.Cabin != award.Economy && q.Cabin != award.PremiumEconomy && q.Cabin != award.Business) || cash.Query.Provider != q.Provider || cash.Query.Mode != "" || cash.Query.Origin != q.Origin || cash.Query.Destination != q.Destination || cash.Query.Date != q.Date || cash.Query.Adults != q.Adults || cash.Query.Cabin != q.Cabin || len(target.Segments) == 0 {
		return award.Itinerary{}, award.Fare{}, false
	}
	first, last := target.Segments[0], target.Segments[len(target.Segments)-1]
	if first.Origin != q.Origin || last.Destination != q.Destination || !strings.HasPrefix(first.DepartureLocal, q.Date+"T") {
		return award.Itinerary{}, award.Fare{}, false
	}
	for _, segment := range target.Segments {
		if segment.Cabin != q.Cabin || canonicalAwardFlight(segment.FlightNumber) == "" || !validCashTaxSegmentTimes(segment) {
			return award.Itinerary{}, award.Fare{}, false
		}
	}
	var match award.Itinerary
	found := false
	for _, itinerary := range cash.Itineraries {
		if !sameCashTaxFlights(target, itinerary) {
			continue
		}
		if found {
			return award.Itinerary{}, award.Fare{}, false
		}
		match, found = itinerary, true
	}
	if !found {
		return award.Itinerary{}, award.Fare{}, false
	}

	for _, fare := range match.Fares {
		if validCashTaxFare(fare, q.Cabin) {
			return match, fare, true
		}
	}
	return award.Itinerary{}, award.Fare{}, false
}

func validCashTaxSegmentTimes(segment award.Segment) bool {
	if segment.Origin == "" || segment.Destination == "" {
		return false
	}
	for _, local := range []string{segment.DepartureLocal, segment.ArrivalLocal} {
		if _, err := time.Parse("2006-01-02T15:04:05", local); err != nil {
			return false
		}
	}
	return true
}

func sameCashTaxFlights(target, candidate award.Itinerary) bool {
	if len(target.Segments) != len(candidate.Segments) {
		return false
	}
	for i, wanted := range target.Segments {
		actual := candidate.Segments[i]
		if canonicalAwardFlight(actual.FlightNumber) != canonicalAwardFlight(wanted.FlightNumber) || actual.Origin != wanted.Origin || actual.Destination != wanted.Destination || actual.DepartureLocal != wanted.DepartureLocal || actual.ArrivalLocal != wanted.ArrivalLocal || (actual.Cabin != "" && actual.Cabin != wanted.Cabin) {
			return false
		}
		wantedOperator, actualOperator := cashTaxOperator(wanted), cashTaxOperator(actual)
		if wantedOperator != "" && actualOperator != "" && wantedOperator != actualOperator {
			return false
		}
	}
	return true
}

func cashTaxOperator(segment award.Segment) string {
	operator := segment.OperatingAirline
	if strings.TrimSpace(operator) == "" {
		operator = segment.Airline
	}
	operator = strings.ToUpper(strings.Join(strings.Fields(operator), " "))
	switch operator {
	case "BR", "EVA AIR", "EVA AIRWAYS", "EVA AIRWAYS CORPORATION":
		return "BR"
	case "B7", "UNI AIR", "UNI AIRWAYS":
		return "B7"
	}
	return operator
}

func validCashTaxFare(fare award.Fare, cabin award.Cabin) bool {
	if fare.Cabin != cabin || strings.TrimSpace(fare.QuoteID) == "" || fare.CashPrice == nil || len(fare.CashPrice.Currency) != 3 {
		return false
	}
	for _, r := range fare.CashPrice.Currency {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	if _, err := partyCashAmount(fare.CashPrice.Amount, 1); err != nil {
		return false
	}
	amount, ok := new(big.Rat).SetString(fare.CashPrice.Amount)
	return ok && amount.Sign() > 0
}

func validateCashTaxSelection(raw json.RawMessage, target award.Itinerary, q award.Query) error {
	mismatch := func() error {
		return award.Failure("search_mismatch", "EVA selected different flights or cabins for the tax estimate")
	}
	if q.Provider != "eva" || q.Mode != "award" || q.Adults < 1 || q.Adults > 6 || len(target.Segments) == 0 {
		return mismatch()
	}
	first, last := target.Segments[0], target.Segments[len(target.Segments)-1]
	if first.Origin != q.Origin || last.Destination != q.Destination || !strings.HasPrefix(first.DepartureLocal, q.Date+"T") {
		return mismatch()
	}
	for _, segment := range target.Segments {
		if segment.Cabin != q.Cabin || canonicalAwardFlight(segment.FlightNumber) == "" || !validCashTaxSegmentTimes(segment) {
			return mismatch()
		}
	}
	var view struct {
		Items []struct {
			Deleted    bool `json:"isDeleted"`
			Flown      bool `json:"flown"`
			NoShow     bool `json:"noShow"`
			Waitlisted bool `json:"isWaitlisted"`
			FareFamily struct {
				Name string `json:"ffName"`
			} `json:"fareFamily"`
			Segments []struct {
				flightSegment
				Cabins []struct {
					Name       string
					Waitlisted bool `json:"isWaitlisted"`
				} `json:"listCabin"`
			} `json:"listSegment"`
		} `json:"listItineraryElem"`
	}
	if json.Unmarshal(raw, &view) != nil || len(view.Items) != 1 || len(view.Items[0].Segments) == 0 {
		return award.Failure("pricing_protocol", "EVA did not confirm the selected itinerary for the tax estimate")
	}
	item := view.Items[0]
	if item.Deleted || item.Flown || item.NoShow || item.Waitlisted || (item.FareFamily.Name != "" && familyCabin(item.FareFamily.Name) != q.Cabin) {
		return mismatch()
	}
	selected := award.Itinerary{}
	for _, segment := range item.Segments {
		if (segment.Airline.Code != "BR" && segment.Airline.Code != "B7") || (segment.OpAirline.Code != "" && segment.OpAirline.Code != "BR" && segment.OpAirline.Code != "B7") || len(segment.Cabins) != 1 || segment.Cabins[0].Waitlisted || familyCabin(segment.Cabins[0].Name) != q.Cabin {
			return mismatch()
		}
		depart, err := localTime(segment.BeginDate)
		if err != nil {
			return mismatch()
		}
		arrive, err := localTime(segment.EndDate)
		if err != nil {
			return mismatch()
		}
		airline, operator := segment.Airline.Name, segment.OpAirline.Name
		if strings.TrimSpace(airline) == "" {
			airline = segment.Airline.Code
		}
		if strings.TrimSpace(operator) == "" {
			operator = segment.OpAirline.Code
		}
		selected.Segments = append(selected.Segments, award.Segment{Cabin: q.Cabin, FlightNumber: segment.Airline.Code + segment.FlightNumber, Airline: airline, OperatingAirline: operator, Origin: segment.BeginLocation.LocationCode, Destination: segment.EndLocation.LocationCode, DepartureLocal: depart, ArrivalLocal: arrive})
	}
	if !sameCashTaxFlights(target, selected) {
		return mismatch()
	}
	return nil
}
