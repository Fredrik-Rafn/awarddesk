package eva

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"awarddesk/internal/award"
	"golang.org/x/net/html"
)

type awardChoice struct {
	itinerary  award.Itinerary
	radioName  string
	radioValue string
}

func awardDescription(n *html.Node, label string) string {
	for _, dt := range awardNodes(n, func(n *html.Node) bool { return n.Data == "dt" }) {
		if !strings.EqualFold(awardText(dt), label) {
			continue
		}
		for dd := dt.NextSibling; dd != nil; dd = dd.NextSibling {
			if dd.Data == "dd" {
				return awardText(dd)
			}
			if dd.Type == html.ElementNode {
				break
			}
		}
	}
	return ""
}

func parseAwardSegments(root *html.Node, cabin award.Cabin) ([]award.Segment, error) {
	var segments []award.Segment
	hasRequestedCabin := false
	rank := map[award.Cabin]int{award.Economy: 1, award.PremiumEconomy: 2, award.Business: 3}
	for _, part := range awardNodes(root, func(n *html.Node) bool { return awardClass(n, "trafficInformation--flightsItem") }) {
		airports := awardNodes(part, func(n *html.Node) bool { return awardClass(n, "flightSegment-airport--mini") })
		segmentCabin := familyCabin(awardDescription(part, "Class"))
		if len(airports) != 2 || rank[segmentCabin] == 0 || rank[segmentCabin] > rank[cabin] {
			return nil, award.Failure("award_protocol", "EVA award segment or cabin changed")
		}
		hasRequestedCabin = hasRequestedCabin || segmentCabin == cabin
		var station [2]string
		var dates [2]string
		for i, airport := range airports {
			text := awardText(airport)
			date, ok := parseAwardDate(text, true)
			prefix := awardDisplayDate.Split(text, 2)[0]
			codes := awardAirportCode.FindAllString(prefix, -1)
			if !ok || len(codes) == 0 {
				return nil, award.Failure("award_protocol", "EVA award segment time or airport changed")
			}
			station[i], dates[i] = codes[len(codes)-1], date
		}
		flight := awardDescription(part, "Flight Number")
		if canonicalAwardFlight(flight) == "" {
			return nil, award.Failure("award_protocol", "Unsupported EVA award operating flight")
		}
		var aircraft *string
		if value := awardDescription(part, "Aircraft"); value != "" {
			aircraft = &value
		}
		airline := "EVA Air"
		if strings.HasPrefix(flight, "B7") {
			airline = "UNI Air"
		}
		segments = append(segments, award.Segment{Cabin: segmentCabin, FlightNumber: flight, Airline: airline, Origin: station[0], Destination: station[1], DepartureLocal: dates[0], ArrivalLocal: dates[1], Aircraft: aircraft})
	}
	if len(segments) == 0 || !hasRequestedCabin {
		return nil, award.Failure("award_protocol", "EVA award segments were not found")
	}
	for i := 1; i < len(segments); i++ {

		if segments[i-1].Destination == segments[i].Origin && segments[i-1].ArrivalLocal > segments[i].DepartureLocal {
			return nil, award.Failure("search_mismatch", "EVA returned inconsistent award connections")
		}
	}
	return segments, nil
}

func parseAwardChoices(body []byte, q award.Query, available map[string]bool) ([]awardChoice, error) {
	root, err := awardDocument(body)
	if err != nil {
		return nil, err
	}
	cards := awardNodes(root, func(n *html.Node) bool { return awardClass(n, "trafficChoose") })
	if len(cards) == 0 {
		return nil, award.Failure("award_protocol", "EVA award flight choices were not found")
	}
	var choices []awardChoice
	seen := map[string]bool{}
	for _, card := range cards {

		dates := awardNodes(card, func(n *html.Node) bool {
			return n.Data == "input" && strings.HasSuffix(awardAttr(n, "name"), "$hiddenDepDate")
		})
		if len(dates) == 1 && awardAttr(dates[0], "value") != strings.ReplaceAll(q.Date, "-", "") {
			continue
		}
		var listed []string
		for _, label := range awardNodes(card, func(n *html.Node) bool { return n.Data == "dt" && awardText(n) == "Flight Number" }) {
			for dd := label.NextSibling; dd != nil; dd = dd.NextSibling {
				if dd.Data == "dd" {
					listed = append(listed, awardText(dd))
					break
				}
			}
		}
		known := len(listed) > 0
		for _, flight := range listed {
			known = known && canonicalAwardFlight(flight) != ""
		}
		if known && !available[awardFlightKey(listed)] {
			continue
		}
		segments, err := parseAwardSegments(card, q.Cabin)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(segments[0].DepartureLocal, q.Date) {
			continue
		}
		if segments[0].Origin != q.Origin || segments[len(segments)-1].Destination != q.Destination {
			return nil, award.Failure("search_mismatch", "EVA award flights do not match the requested route")
		}
		var flights []string
		for _, s := range segments {
			flights = append(flights, s.FlightNumber)
		}
		if !available[awardFlightKey(flights)] {
			continue
		}
		statuses := awardNodes(card, func(n *html.Node) bool { return awardClass(n, "flightSegment-status") })
		if len(statuses) != len(segments) {
			return nil, award.Failure("award_protocol", "EVA did not report every award segment's status")
		}
		confirmed := true
		for _, status := range statuses {
			label := awardText(status)
			switch strings.ToLower(label) {
			case "status available(unconfirmed)":
			case "status waiting", "status ｗaiting", "status unavailable", "status not available":
				confirmed = false
			default:

				logAwardStatusDiagnostic(q, "flight selection", label, "")
				return nil, award.Failure("award_protocol", "EVA returned an unknown award flight-selection status")
			}
		}
		if !confirmed {
			continue
		}
		radios := awardNodes(card, func(n *html.Node) bool { return n.Data == "input" && awardAttr(n, "type") == "radio" })
		if len(radios) != 1 || awardAttr(radios[0], "name") == "" {
			return nil, award.Failure("award_protocol", "EVA award selection control changed")
		}
		name, value := awardAttr(radios[0], "name"), awardAttr(radios[0], "value")
		if value == "" {
			value = "rb_Choice"
		}
		id := awardFlightKey(flights) + "@" + segments[0].DepartureLocal
		if seen[id] {
			return nil, award.Failure("award_protocol", "EVA returned duplicate award choices")
		}
		seen[id] = true
		choices = append(choices, awardChoice{itinerary: award.Itinerary{ID: id, Segments: segments, Fares: []award.Fare{}, Summary: "EVA award ticket"}, radioName: name, radioValue: value})
	}
	return choices, nil
}

func selectAwardChoice(f *form, choice awardChoice) error {

	for name := range f.values {
		if strings.Contains(name, "$rpt_Air_MultiAvailability$") && strings.HasSuffix(name, "$") {
			delete(f.values, name)
		}
	}
	if f.values.Get(choice.radioName+"hiddenSelectValue") == "" {
		return award.Failure("award_protocol", "EVA award selection token is missing")
	}
	f.values.Set(choice.radioName, choice.radioValue)
	if err := f.set("__MasterEVENTTARGET", "btn_Confirm_Click"); err != nil {
		return err
	}
	return f.set("__MasterEVENTARGUMENT", "")
}

var awardMileageNumber = regexp.MustCompile(`^(?:[1-9][0-9]*|[1-9][0-9]{0,2}(?:,[0-9]{3})+)$`)

func parseAwardMileage(body []byte, choice awardChoice, q award.Query) (int, error) {
	root, err := awardDocument(body)
	if err != nil {
		return 0, err
	}
	segments, err := parseAwardSegments(root, q.Cabin)
	if err != nil {
		return 0, err
	}
	if len(segments) != len(choice.itinerary.Segments) {
		return 0, award.Failure("search_mismatch", "EVA mileage quote changed the itinerary")
	}
	for i, s := range segments {
		wanted := choice.itinerary.Segments[i]
		if s.Cabin != wanted.Cabin || canonicalAwardFlight(s.FlightNumber) != canonicalAwardFlight(wanted.FlightNumber) || s.Origin != wanted.Origin || s.Destination != wanted.Destination || s.DepartureLocal != wanted.DepartureLocal || s.ArrivalLocal != wanted.ArrivalLocal {
			return 0, award.Failure("search_mismatch", "EVA mileage quote does not match the selected flight")
		}
	}
	value := awardDescription(root, "Mileage deducted for one person")
	if !awardMileageNumber.MatchString(value) {
		return 0, award.Failure("award_protocol", "EVA did not return a valid per-person award mileage quote")
	}
	miles, err := strconv.Atoi(strings.ReplaceAll(value, ",", ""))
	if err != nil || miles > 10000000 {
		return 0, award.Failure("award_protocol", "EVA award mileage amount is out of range")
	}
	return miles * q.Adults, nil
}

func allowedAwardPage(target, page string) bool {
	u, err := url.Parse(target)
	return err == nil && u.Scheme == "https" && u.Host == "booking.evaair.com" && u.User == nil && strings.EqualFold(u.Path, awardTicketPath+page)
}
