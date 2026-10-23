package eva

import "awarddesk/internal/award"

type fareSelection struct {
	flight award.Itinerary
	fare   award.Fare
}

func unpricedFares(result award.Result) []fareSelection {
	seen := make(map[string]bool)
	var plan []fareSelection
	for _, flight := range result.Itineraries {
		for _, fare := range flight.Fares {
			if fare.MilesCash != nil || (fare.MilesCashEligible != nil && !*fare.MilesCashEligible) || seen[fare.QuoteID] {
				continue
			}
			seen[fare.QuoteID] = true
			plan = append(plan, fareSelection{flight, fare})
		}
	}
	return plan
}
