package ba

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	"golang.org/x/net/html"

	"awarddesk/internal/award"
)

func cabinCode(c award.Cabin) string {
	return map[award.Cabin]string{award.Economy: "M", award.PremiumEconomy: "W", award.Business: "C", award.First: "F"}[c]
}
func localDate(label, clock string, search time.Time) string {
	d, e := time.Parse("2 Jan 2006", label+" "+strconv.Itoa(search.Year()))
	if e != nil {
		return label + " " + clock
	}
	if d.Before(search.AddDate(0, -6, 0)) {
		d = d.AddDate(1, 0, 0)
	}
	return d.Format("2006-01-02") + "T" + clock + ":00"
}
func normalize(a Award, q award.Query, d time.Time) award.Itinerary {
	sum := sha256.Sum256([]byte(a.Selection + string(q.Cabin)))
	it := award.Itinerary{ID: hex.EncodeToString(sum[:8]), Summary: a.Summary, Segments: []award.Segment{}, Fares: []award.Fare{}}
	for _, s := range a.Segments {
		it.Segments = append(it.Segments, award.Segment{FlightNumber: s.Flight, Airline: s.Airline, Origin: s.Origin, Destination: s.Destination, DepartureLocal: localDate(s.DepartureDate, s.DepartureTime, d), ArrivalLocal: localDate(s.ArrivalDate, s.ArrivalTime, d)})
	}
	return it
}

var quotePattern = regexp.MustCompile(`([\d,]+) Avios\s*\+\s*([\d,.]+)\s+([A-Z]{3})`)
var cashPattern = regexp.MustCompile(`^\d+(?:\.\d{1,2})?$`)

func fares(body []byte, c award.Cabin, seats *int) ([]award.Fare, bool) {
	doc, e := html.Parse(bytes.NewReader(body))
	if e != nil {
		return nil, false
	}
	out := []award.Fare{}
	seen := map[string]bool{}
	appendQuote := func(n *html.Node, name string) {
		m := quotePattern.FindStringSubmatch(txt(n))
		if m == nil {
			return
		}
		points, err := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
		if err != nil || points <= 0 {
			return
		}
		cash := strings.ReplaceAll(m[2], ",", "")
		if !cashPattern.MatchString(cash) {
			return
		}
		key := m[1] + "/" + cash + "/" + m[3]
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, award.Fare{Name: name, Cabin: c, Points: points, Program: "Avios", Taxes: award.Money{Amount: cash, Currency: m[3]}, Seats: seats, Estimated: true})
	}
	walk(doc, func(n *html.Node) {
		if cls(n, "totalPriceAviosTxt") {
			appendQuote(n, "Displayed award")
		}
		if attr(n, "id") == "lstMrePrccngSB" || attr(n, "id") == "lstMrePrcngSB" {
			walk(n, func(li *html.Node) {
				if li.Data == "li" {
					appendQuote(li, "Alternative Avios option")
				}
			})
		}
	})
	missing := false
	walk(doc, func(n *html.Node) {
		if attr(n, "id") == "hdnMrePrceCnt" {
			v, _ := strconv.Atoi(attr(n, "value"))
			missing = v > len(out)
		}
	})
	return out, missing
}
func (r *runner) search(q award.Query) (award.Result, error) {
	d, _ := time.Parse("2006-01-02", q.Date)
	result := award.Result{Query: q, FetchedAt: time.Now().UTC(), Itineraries: []award.Itinerary{}, Warnings: []string{"BA displays estimated prices; exact cost follows Continue.", "Fare options revealed only during later booking steps may be unavailable."}, Complete: true}
	u, _ := url.Parse("https://www.britishairways.com/")
	r.client.SetCookies(u, []*http.Cookie{{Name: "token", Value: r.classic.Access, Path: "/", Secure: true, MaxAge: 86400}, {Name: "refreshtoken", Value: r.classic.Refresh, Path: "/", Secure: true, MaxAge: 86400}})
	direction := "/travel/redeem/execclub/public/en_dk?eid=106019&tab_selected=redeem&redemption_type=std_red"
	if _, e := r.navigate("https://www.britishairways.com" + direction); e != nil {
		return result, e
	}
	v := url.Values{"Directional_Login": {direction}, "accesstoken": {r.classic.Access}, "AngularAuthenticationPassThrough": {"True"}}
	res, _, e := r.request("POST", "https://www.britishairways.com/travel/loginr/public/en_dk?eId=109010", strings.NewReader(v.Encode()), http.Header{"content-type": {"application/x-www-form-urlencoded"}, "origin": {"https://www.britishairways.com"}, "referer": {"https://www.britishairways.com" + direction}})
	if e != nil {
		return result, e
	}
	if res.StatusCode != 302 {
		return result, errors.New("booking session did not redirect")
	}
	next, e := res.Location()
	if e != nil {
		return result, errors.New("booking redirect missing")
	}
	b, e := r.navigate(next.String())
	if e != nil {
		return result, e
	}
	v, e = hiddenFields(b, "plan_redeem_trip")
	if e != nil {
		return result, e
	}
	if v.Get("FormToken") == "" {
		return result, errors.New("fresh search form token missing")
	}
	for k, value := range map[string]string{"DEVICE_TYPE": "DESKTOP", "eId": "100002", "pageid": "PLANREDEMPTIONJOURNEY", "tab_selected": "redeem", "redemption_type": "STD_RED", "WebApplicationID": "BOD", "departurePoint": q.Origin, "destinationPoint": q.Destination, "oneWay": "true", "CabinCode": cabinCode(q.Cabin), "RestrictionType": "Restricted", "NumberOfAdults": strconv.Itoa(q.Adults), "NumberOfYoungAdults": "0", "NumberOfChildren": "0", "NumberOfInfants": "0", "departInputDate": d.Format("02/01/06")} {
		v.Set(k, value)
	}
	r.progress("availability", "Checking award availability")
	b, e = r.postAward(awardBase, v)
	if e != nil {
		return result, e
	}
	v, e = continuationFields(b)
	if e != nil {
		return result, e
	}
	b, e = r.postAward(awardBase, v)
	if e != nil {
		return result, e
	}
	raw, e := parseAwards(b, cabinCode(q.Cabin))
	if e != nil {
		doc, _ := html.Parse(bytes.NewReader(b))
		sold := first(doc, func(n *html.Node) bool {
			return attr(n, "id") == "soldOut" && strings.EqualFold(attr(n, "value"), "true")
		})
		if sold != nil {
			return result, nil
		}
		return result, award.Failure("unexpected_availability", "BA availability could not be read: "+e.Error())
	}
	v, e = hiddenFields(b, "")
	if e != nil {
		return result, e
	}
	token := v.Get("FormToken")
	if token == "" {
		return result, errors.New("pricing form token missing")
	}
	if len(raw) > 60 {
		result.Complete = false
		result.Warnings = append(result.Warnings, "More than 60 itineraries returned; search is capped at 60 quotes.")
		raw = raw[:60]
	}
	for i, a := range raw {
		if r.report != nil {
			r.report(award.Progress{Stage: "pricing", Message: fmt.Sprintf("Pricing itinerary %d of %d", i+1, len(raw)), Completed: i, Total: len(raw)})
		}
		p := url.Values{"0": {a.Selection}, "eId": {"100004"}, "Stopover": {"false"}, "HostAirlineCode": {"BA"}, "BAOnlyRoute": {"false"}, "BANotOperateFullOrPartial": {"true"}, "HostAirline": {"British Airways"}, "FormToken": {token}}
		for _, key := range []string{"Stopover", "HostAirlineCode", "BAOnlyRoute", "BANotOperateFullOrPartial", "HostAirline"} {
			if values, ok := v[key]; ok {
				p[key] = values
			}
		}
		body, e := r.postAward(awardBase+"/device-all", p)
		if e != nil {
			return result, e
		}
		it := normalize(a, q, d)
		var missing bool
		it.Fares, missing = fares(body, q.Cabin, a.SeatsShown)
		if len(it.Fares) == 0 {
			return result, award.Failure("missing_quote", "BA did not return an award price for a selected flight")
		}
		if missing {
			result.Complete = false
			result.Warnings = append(result.Warnings, "BA indicated more fare options than this response exposed for "+it.ID)
		}
		result.Itineraries = append(result.Itineraries, it)
		doc, _ := html.Parse(bytes.NewReader(body))
		n := first(doc, func(n *html.Node) bool { return attr(n, "data-formtoken") != "" })
		if n != nil {
			token = attr(n, "data-formtoken")
		}
	}
	if q.Cabin != award.Economy {
		result.Warnings = append(result.Warnings, "This cabin mapping has parser coverage but has not been verified with a live search.")
	}
	if e := r.enrich(&result, raw); e != nil {
		return result, e
	}
	result.FetchedAt = time.Now().UTC()
	return result, nil
}
