package eva

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"awarddesk/internal/award"
)

type pageData struct {
	SessionID  string                     `json:"-"`
	PageTicket string                     `json:"pageTicket"`
	Business   map[string]json.RawMessage `json:"business"`
	Basefacts  map[string]string          `json:"basefacts"`
	Config     map[string]json.RawMessage `json:"config"`
}

var pageSessionID = regexp.MustCompile(`\bsessionId\s*:\s*["']([A-Za-z0-9_!.~-]{1,256})["']`)

// Read EVA's booking data from JSON responses or the initial HTML page.
// Parse the embedded JSON directly; never execute the page's JavaScript.
func decodePage(body []byte) (pageData, error) {
	var p pageData
	var envelope struct {
		Data      pageData `json:"data"`
		SessionID string   `json:"sessionId"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Data.Business != nil {
		envelope.Data.SessionID = envelope.SessionID
		return envelope.Data, nil
	}
	var navigation struct {
		Data struct {
			PageDefinitionConfig struct {
				PageData pageData `json:"pageData"`
			} `json:"pageDefinitionConfig"`
		} `json:"data"`
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(body, &navigation) == nil && navigation.Data.PageDefinitionConfig.PageData.Business != nil {
		p = navigation.Data.PageDefinitionConfig.PageData
		p.SessionID = navigation.SessionID
		return p, nil
	}
	start := bytes.Index(body, []byte(`{"siteConfiguration"`))
	if start < 0 {
		return p, award.Failure("search_protocol", "EVA booking data was not found")
	}
	var bootstrap struct {
		PageDefinitionConfig struct {
			PageData pageData `json:"pageData"`
		} `json:"pageDefinitionConfig"`
	}
	if err := json.NewDecoder(bytes.NewReader(body[start:])).Decode(&bootstrap); err != nil || bootstrap.PageDefinitionConfig.PageData.Business == nil {
		return p, award.Failure("search_protocol", "EVA booking data format changed")
	}
	p = bootstrap.PageDefinitionConfig.PageData
	// The navigation session is a sibling of config in the initializer.
	// siteConfiguration.appData.beSessionId belongs to analytics and differs.
	matches := pageSessionID.FindAllSubmatch(body, -1)
	if len(matches) == 1 {
		p.SessionID = string(matches[0][1])
	}
	return p, nil
}

type location struct {
	LocationCode string `json:"locationCode"`
}
type namedCode struct{ Name, Code string }
type flightSegment struct {
	BeginLocation                    location
	EndLocation                      location
	BeginDate, EndDate, FlightNumber string
	Airline, OpAirline, Equipment    namedCode
}
type availability struct {
	ProposedBounds []struct {
		ProposedFlightsGroup []struct {
			ProposedBoundID int `json:"proposedBoundId"`
			Segments        []flightSegment
		}
	}
	FareFamilyList     []struct{ FFCode, FFName string }
	RecommendationList []struct {
		RecoID     int `json:"recoId"`
		FFCode     string
		RecoAmount struct{ TotalAmount json.Number }
		Bounds     []struct {
			FlightGroupList []struct {
				FlightID          int `json:"flightId"`
				NumberOfSeatsLeft int
				RBD               string
			}
		}
	}
	CurrencyBean namedCode
}

var tags = regexp.MustCompile(`<[^>]*>`)

func plainName(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tags.ReplaceAllString(s, " "))), " ")
}
func familyCabin(s string) award.Cabin {
	s = strings.ToLower(plainName(s))
	switch {
	case strings.Contains(s, "premium economy"):
		return award.PremiumEconomy
	case strings.Contains(s, "business"), strings.Contains(s, "royal laurel"), strings.Contains(s, "premium laurel"):
		return award.Business
	case strings.Contains(s, "economy"):
		return award.Economy
	default:
		return ""
	}
}
func localTime(s string) (string, error) {
	for _, layout := range []string{"Jan 2, 2006, 3:04:05 PM", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02T15:04:05"), nil
		}
	}
	return "", award.Failure("search_protocol", "EVA flight time format changed")
}
func parseAvailability(raw json.RawMessage, q award.Query) (award.Result, error) {
	r := award.Result{Query: q, FetchedAt: time.Now().UTC(), Itineraries: []award.Itinerary{}, Warnings: []string{}}
	var a availability
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&a) != nil || len(a.ProposedBounds) != 1 || len(a.CurrencyBean.Code) != 3 {
		return r, award.Failure("search_protocol", "EVA availability response is unsupported")
	}
	families := map[string]string{}
	for _, f := range a.FareFamilyList {
		families[f.FFCode] = plainName(f.FFName)
	}
	flights := map[int]*award.Itinerary{}
	eligible := map[int]bool{}
	for _, f := range a.ProposedBounds[0].ProposedFlightsGroup {
		eligible[f.ProposedBoundID] = true
		item := &award.Itinerary{ID: strconv.Itoa(f.ProposedBoundID), Fares: []award.Fare{}, Summary: "EVA Miles & Cash"}
		for _, s := range f.Segments {
			if (s.Airline.Code != "BR" && s.Airline.Code != "B7") || (s.OpAirline.Code != "" && s.OpAirline.Code != "BR" && s.OpAirline.Code != "B7") {
				eligible[f.ProposedBoundID] = false
			}
			depart, err := localTime(s.BeginDate)
			if err != nil {
				return r, err
			}
			arrive, err := localTime(s.EndDate)
			if err != nil {
				return r, err
			}
			var aircraft *string
			if s.Equipment.Name != "" {
				name := s.Equipment.Name
				aircraft = &name
			}
			item.Segments = append(item.Segments, award.Segment{FlightNumber: s.Airline.Code + s.FlightNumber, Airline: s.Airline.Name, OperatingAirline: s.OpAirline.Name, Origin: s.BeginLocation.LocationCode, Destination: s.EndLocation.LocationCode, DepartureLocal: depart, ArrivalLocal: arrive, Aircraft: aircraft})
		}
		if len(item.Segments) == 0 {
			return r, award.Failure("search_protocol", "EVA returned a flight without segments")
		}
		first, last := item.Segments[0], item.Segments[len(item.Segments)-1]
		if first.Origin != q.Origin || last.Destination != q.Destination || !strings.HasPrefix(first.DepartureLocal, q.Date) {
			return r, award.Failure("search_mismatch", "EVA returned a different route or date")
		}
		flights[f.ProposedBoundID] = item
	}
	for _, rec := range a.RecommendationList {
		name, ok := families[rec.FFCode]
		if !ok {
			return r, award.Failure("search_protocol", "EVA fare family missing")
		}
		cabin := familyCabin(name)
		if cabin == "" {
			return r, award.Failure("search_protocol", "EVA fare cabin was not recognized")
		}
		if cabin != q.Cabin {
			continue
		}
		if len(rec.Bounds) != 1 {
			return r, award.Failure("search_protocol", "EVA returned an unsupported bound count")
		}
		amount, err := rec.RecoAmount.TotalAmount.Float64()
		if err != nil || amount < 0 {
			return r, award.Failure("search_protocol", "EVA fare amount missing")
		}
		partyAmount, err := partyCashAmount(rec.RecoAmount.TotalAmount.String(), q.Adults)
		if err != nil {
			return r, award.Failure("search_protocol", "EVA fare amount missing")
		}
		for _, f := range rec.Bounds[0].FlightGroupList {
			item, ok := flights[f.FlightID]
			if !ok {
				return r, award.Failure("search_protocol", "EVA fare references an unknown flight")
			}
			var seats *int
			if f.NumberOfSeatsLeft > 0 {
				n := f.NumberOfSeatsLeft
				seats = &n
			}
			canUseMiles := eligible[f.FlightID] && !strings.EqualFold(strings.TrimSpace(f.RBD), "A")
			item.Fares = append(item.Fares, award.Fare{Name: name, Cabin: cabin, Program: "EVA Infinity MileageLands", PricingType: "miles_cash", QuoteID: strconv.Itoa(rec.RecoID), CashPrice: &award.Money{Amount: partyAmount, Currency: a.CurrencyBean.Code}, Seats: seats, MilesCashEligible: &canUseMiles})
		}
	}
	for _, item := range flights {
		if len(item.Fares) > 0 {
			r.Itineraries = append(r.Itineraries, *item)
		}
	}
	sort.Slice(r.Itineraries, func(i, j int) bool {
		return r.Itineraries[i].Segments[0].DepartureLocal < r.Itineraries[j].Segments[0].DepartureLocal
	})
	r.Warnings = append(r.Warnings, fmt.Sprintf("Found %d flight options. Full cash fares are available; Miles & Cash conversion is not yet quoted.", len(r.Itineraries)))
	return r, nil
}
