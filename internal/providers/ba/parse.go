package ba

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

type Segment struct {
	DetailURL     string `json:"-"`
	Flight        string `json:"flight"`
	Airline       string `json:"airline"`
	Origin        string `json:"origin"`
	Destination   string `json:"destination"`
	DepartureTime string `json:"departureTimeLocal"`
	DepartureDate string `json:"departureDateLabel"`
	ArrivalTime   string `json:"arrivalTimeLocal"`
	ArrivalDate   string `json:"arrivalDateLabel"`
}
type Award struct {
	Selection  string    `json:"selection"`
	Segments   []Segment `json:"segments"`
	Cabin      string    `json:"cabin"`
	SeatsShown *int      `json:"seatsShown,omitempty"`
	Summary    string    `json:"summary"`
}

func attr(n *html.Node, k string) string {
	for _, a := range n.Attr {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}
func cls(n *html.Node, c string) bool { return strings.Contains(" "+attr(n, "class")+" ", " "+c+" ") }
func walk(n *html.Node, f func(*html.Node)) {
	f(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, f)
	}
}
func txt(n *html.Node) string {
	var b strings.Builder
	walk(n, func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
			b.WriteByte(' ')
		}
	})
	return strings.Join(strings.Fields(b.String()), " ")
}
func first(n *html.Node, p func(*html.Node) bool) *html.Node {
	if p(n) {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := first(child, p); found != nil {
			return found
		}
	}
	return nil
}
func classText(n *html.Node, c string) string {
	x := first(n, func(x *html.Node) bool { return cls(x, c) })
	if x == nil {
		return ""
	}
	return txt(x)
}
func parseAwards(body []byte, cabin string) ([]Award, error) {
	doc, e := html.Parse(bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	out := []Award{}
	invalid := false
	reason := "missing selection or segment"
	walk(doc, func(row *html.Node) {
		if !cls(row, "journeyOptions") {
			return
		}
		radio := first(row, func(x *html.Node) bool {
			return cls(x, "cabinSelector") && strings.HasSuffix(attr(x, "id"), "CabinCode"+cabin)
		})
		if radio == nil {
			return
		}
		a := Award{Selection: attr(radio, "value"), Cabin: cabin, Segments: []Segment{}}
		segmentCount := 0
		if n := first(row, func(x *html.Node) bool { return attr(x, "summary") != "" }); n != nil {
			a.Summary = attr(n, "summary")
		}
		walk(row, func(n *html.Node) {
			if !cls(n, "travel-time-detail") {
				return
			}
			segmentCount++
			s := Segment{DepartureTime: classText(n, "departtime"), DepartureDate: classText(n, "departdate"), ArrivalTime: classText(n, "arrivaltime"), ArrivalDate: classText(n, "arrivaldate")}
			walk(n, func(x *html.Node) {
				if strings.HasPrefix(attr(x, "id"), "deptAirportCodeLink") {
					s.Origin = txt(x)
				}
				if strings.HasPrefix(attr(x, "id"), "arrivalAirportCodeLink") {
					s.Destination = txt(x)
				}
				if cls(x, "flightDetailModal") {
					s.DetailURL = attr(x, "href")
					parts := []string{}
					walk(x, func(z *html.Node) {
						if cls(z, "sl_split") {
							parts = append(parts, txt(z))
						}
					})
					if len(parts) == 2 {
						s.Airline = parts[0]
						s.Flight = parts[1]
					}
				}
			})
			if s.Flight != "" {
				a.Segments = append(a.Segments, s)
			}
		})
		if a.Selection == "" || len(a.Segments) == 0 || len(a.Segments) != segmentCount {
			invalid = true
		}
		for _, s := range a.Segments {
			if !airportCode.MatchString(s.Origin) || !airportCode.MatchString(s.Destination) || !clockTime.MatchString(s.DepartureTime) || !clockTime.MatchString(s.ArrivalTime) || s.DepartureDate == "" || s.ArrivalDate == "" {
				invalid = true
				reason = fmt.Sprintf("route recognized=%t, departure time recognized=%t, arrival time recognized=%t, dates present=%t", airportCode.MatchString(s.Origin) && airportCode.MatchString(s.Destination), clockTime.MatchString(s.DepartureTime), clockTime.MatchString(s.ArrivalTime), s.DepartureDate != "" && s.ArrivalDate != "")
			}
		}
		for p := radio.Parent; p != nil && p != row; p = p.Parent {
			if cls(p, "cabin-data") {
				n := first(p, func(x *html.Node) bool { return strings.Contains(attr(x, "aria-label"), "seats left") })
				if n != nil {
					fields := strings.Fields(attr(n, "aria-label"))
					if len(fields) > 0 {
						v, err := strconv.Atoi(fields[0])
						if err == nil {
							a.SeatsShown = &v
						}
					}
				}
				break
			}
		}
		out = append(out, a)
	})
	if invalid {
		return nil, fmt.Errorf("availability contains incomplete flight segments (%s)", reason)
	}
	if len(out) == 0 {
		return nil, errors.New("no selectable award itineraries found; cannot distinguish unavailable inventory from an unexpected page")
	}
	return out, nil
}

var airportCode = regexp.MustCompile(`^[A-Z]{3}$`)
var clockTime = regexp.MustCompile(`^(?:[01]\d|2[0-3]):[0-5]\d$`)
