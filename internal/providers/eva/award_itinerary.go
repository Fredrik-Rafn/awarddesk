package eva

import (
	"context"
	"strings"

	"awarddesk/internal/award"
)

func (p *Provider) loadAwardJourneys(ctx context.Context, q award.Query, nav awardNavigation) ([]byte, string, error) {
	body, target, err := nav(ctx, "GET", awardItineraryURL, nil, awardAvailabilityURL)
	if err != nil {
		return nil, target, err
	}
	f, err := parseForm(body, target)
	if err != nil || !allowedAwardPage(f.action, "select-itinerary.aspx") {
		return nil, target, award.Failure("award_protocol", "EVA award itinerary form was not found")
	}
	if err = fillAwardItinerary(&f, q, p.awardAirports.Airports); err != nil {
		return nil, target, err
	}
	body, target, err = nav(ctx, "POST", f.action, f.values, target)
	if err != nil {
		return nil, target, err
	}
	return followAwardBridge(ctx, body, target, "select-first-journey.aspx", nav)
}

func fillAwardItinerary(f *form, q award.Query, airports []awardAirport) error {
	fields := map[string]string{"$rb_trip": "1", "$DepNA": q.Origin, "$ArrNA": q.Destination, "$hid_DepCity": q.Origin, "$hid_ArrCity": q.Destination, "$ddl_Cabin": map[award.Cabin]string{award.Economy: "X", award.PremiumEconomy: "E", award.Business: "I"}[q.Cabin], "$txt_OneWayDepDate": strings.ReplaceAll(q.Date, "-", "/"), "$hid_tbGoYYYYMM": strings.ReplaceAll(q.Date, "-", "/"), "__MasterEVENTTARGET": "", "__MasterEVENTARGUMENT": ""}
	for _, side := range []struct{ prefix, code string }{{"Dep", q.Origin}, {"Arr", q.Destination}} {
		found := false
		for _, airport := range airports {
			if airport.Code == side.code && airport.Continent != "" && airport.Community != "" {
				fields["$hid_"+side.prefix+"ContinentCode"], fields["$hid_"+side.prefix+"CommunityCode"] = airport.Continent, airport.Community
				found = true
				break
			}
		}
		if !found {
			return award.Failure("unsupported_route", "Airport is missing from EVA's award booking directory")
		}
	}
	for suffix, value := range fields {
		if err := f.set(suffix, value); err != nil {
			return award.Failure("award_protocol", "EVA award itinerary fields changed")
		}
	}
	f.values.Set("ctl00$content$btn_ok", "")
	return nil
}

func followAwardBridge(ctx context.Context, body []byte, target, destination string, nav awardNavigation) ([]byte, string, error) {
	f, err := parseForm(body, target)
	if err != nil {
		captureAwardFailure(body)
		return nil, target, award.Failure("award_protocol", "EVA award page has no form")
	}
	if !f.values.Has("Parameter") {
		return body, target, nil
	}
	if !allowedAwardPage(f.action, destination) || f.values.Get("Parameter") == "" || len(f.values) != 1 {
		return nil, target, award.Failure("unsafe_destination", "EVA award handoff changed")
	}
	return nav(ctx, "POST", f.action, f.values, target)
}
