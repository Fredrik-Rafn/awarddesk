package eva

import (
	"strconv"
	"strings"

	"awarddesk/internal/award"
	"golang.org/x/net/html"
)

const awardAvailabilityURL = "https://booking.evaair.com/flyeva/eva/b2c/plan-your-journey/award-upgrade-availability/award-upgrade-availability.aspx"

func fillAwardAvailability(f *form, q award.Query) error {
	cabin := map[award.Cabin]string{award.Economy: "EY", award.PremiumEconomy: "PE", award.Business: "SD"}[q.Cabin]
	if cabin == "" {
		return award.Failure("unsupported", "Unsupported EVA award cabin")
	}
	for suffix, value := range map[string]string{
		"$hid_Dep": q.Origin, "$hid_Arr": q.Destination,
		"$rbn_SearchType": "award", "$rbn_Segment": "ONE_WAY",
		"$txt_formcity": q.Origin, "$txt_tocity": q.Destination,
		"$txt_tbGo2YYYYMM": strings.ReplaceAll(q.Date, "-", "/"),
		"$ddl_Award_Cabin": cabin, "$ddl_PassengerCount": strconv.Itoa(q.Adults),
		"__MasterEVENTTARGET": "ctl00$content$btn_ok", "__MasterEVENTARGUMENT": "",
	} {
		if err := f.set(suffix, value); err != nil {
			return award.Failure("award_protocol", "EVA award availability fields changed")
		}
	}
	return nil
}

func parseAwardAvailability(body []byte, q award.Query) (map[string]bool, error) {
	f, err := parseForm(body, awardAvailabilityURL)
	if err != nil || f.values.Get(f.field("$hid_Dep")) != q.Origin || f.values.Get(f.field("$hid_Arr")) != q.Destination || f.values.Get(f.field("$ddl_PassengerCount")) != strconv.Itoa(q.Adults) || f.values.Get(f.field("$rbn_SearchType")) != "award" || f.values.Get(f.field("$ddl_Award_Cabin")) != map[award.Cabin]string{award.Economy: "EY", award.PremiumEconomy: "PE", award.Business: "SD"}[q.Cabin] {
		return nil, award.Failure("search_mismatch", "EVA award availability did not confirm the requested route, cabin and party")
	}
	root, err := awardDocument(body)
	if err != nil {
		return nil, err
	}

	grids := awardNodes(root, func(n *html.Node) bool {
		return awardAttr(n, "id") == "content_control_Award_AvailabilityGO_div_Result"
	})
	main := awardNodes(root, func(n *html.Node) bool { return awardAttr(n, "id") == "mainContent" })
	if len(grids) == 0 && len(main) == 1 && strings.Contains(awardText(main[0]), "The class of service you want to book is not available on this route or we do not have a flight on the day you want to travel.") && strings.ReplaceAll(f.values.Get(f.field("$txt_tbGo2YYYYMM")), "/", "-") == q.Date && f.values.Get(f.field("$rbn_Segment")) == "ONE_WAY" {
		return map[string]bool{}, nil
	}
	if len(grids) != 1 {
		return nil, award.Failure("award_protocol", "EVA award availability grid was not found")
	}
	available := map[string]bool{}
	matchedDate := false
	for _, row := range awardNodes(grids[0], func(n *html.Node) bool { return n.Data == "tr" }) {
		var flights []string
		for _, number := range awardNodes(row, func(n *html.Node) bool { return awardClass(n, "table-flightNumber") }) {
			v := awardFlightNumber.FindString(awardText(number))
			if v == "" {
				return nil, award.Failure("award_protocol", "EVA award flight number changed")
			}
			flights = append(flights, v)
		}
		if len(flights) == 0 {
			continue
		}
		for _, day := range awardNodes(row, func(n *html.Node) bool { return n.Data == "td" && awardClass(n, "table-day") }) {
			date, ok := parseAwardDate(awardAttr(day, "aria-label"), false)
			if !ok || date != q.Date {
				continue
			}
			matchedDate = true
			icons := awardNodes(day, func(n *html.Node) bool { return n.Data == "img" })
			if len(icons) != 1 {
				return nil, award.Failure("award_protocol", "EVA award status icon changed")
			}
			isAvailable, known := classifyAwardAvailabilityStatus(day, icons[0])
			if !known {
				logUnknownAwardStatus(q, icons[0])
				return nil, award.Failure("award_protocol", "EVA returned an unknown award availability status")
			}
			if isAvailable {
				available[awardFlightKey(flights)] = true
			}
		}
	}
	if !matchedDate {
		return nil, award.Failure("award_protocol", "EVA award grid did not contain the requested date")
	}
	return available, nil
}
