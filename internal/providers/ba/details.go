package ba

import (
	"bytes"
	"errors"
	"net/url"
	"strings"

	"golang.org/x/net/html"

	"awarddesk/internal/award"
)

type flightDetail struct {
	aircraft *string
	operator string
}

// Extract only explicitly labelled values. Missing fields remain unknown.
func parseDetail(body []byte) flightDetail {
	doc, e := html.Parse(bytes.NewReader(body))
	if e != nil {
		return flightDetail{}
	}
	var detail flightDetail
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		label := strings.ToLower(strings.Trim(strings.TrimSpace(txt(n)), ":"))
		aircraft := label == "aircraft" || label == "aircraft type" || label == "equipment"
		operator := label == "operated by" || label == "operating airline"
		if !aircraft && !operator {
			return
		}
		next := n.NextSibling
		for next != nil && strings.TrimSpace(txt(next)) == "" {
			next = next.NextSibling
		}
		if next == nil {
			return
		}
		value := strings.TrimSpace(txt(next))
		// BA sometimes repeats the label inside the value cell for screen
		// readers. Keep the aircraft/operator value, not that duplicated label.
		prefixes := []string{"operated by:", "operating airline:"}
		if aircraft {
			prefixes = []string{"aircraft type:", "aircraft:", "equipment:"}
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(strings.ToLower(value), prefix) {
				value = strings.TrimSpace(value[len(prefix):])
				break
			}
		}
		if value == "" || len(value) > 100 {
			return
		}
		// An empty value can leave a wrapper whose next sibling is another
		// field label. Never report that label as an aircraft/operator.
		switch strings.ToLower(strings.Trim(value, ":")) {
		case "aircraft", "aircraft type", "equipment", "operated by", "operating airline":
			return
		}
		if strings.HasSuffix(value, ":") {
			return
		}
		if aircraft && detail.aircraft == nil {
			detail.aircraft = &value
		}
		if operator && detail.operator == "" {
			detail.operator = value
		}
	})
	return detail
}

// Detail links come from the fresh result page. Restrict them to the read-only
// flight-details event instead of following arbitrary captured links.
func detailURL(raw string) (string, bool) {
	base, _ := url.Parse(awardBase)
	u, e := url.Parse(raw)
	if e != nil || raw == "" {
		return "", false
	}
	u = base.ResolveReference(u)
	if u.Scheme != "https" || u.Host != base.Host || u.Path != base.Path || u.User != nil || u.Query().Get("eId") != "100023" {
		return "", false
	}
	return u.String(), true
}

func (r *runner) enrich(result *award.Result, raw []Award) error {
	cache := map[string]flightDetail{}
	unavailable := false
	for i, a := range raw {
		for j, s := range a.Segments {
			endpoint, ok := detailURL(s.DetailURL)
			if !ok {
				unavailable = true
				continue
			}
			d, ok := cache[endpoint]
			if !ok {
				if len(cache) >= 40 {
					unavailable = true
					continue
				}
				r.progress("details", "Retrieving flight details")
				_, body, e := r.request("GET", endpoint, nil, nil)
				if e != nil {
					if r.ctx.Err() != nil {
						return r.ctx.Err()
					}
					var ae *award.Error
					if errors.As(e, &ae) && (ae.Code == "rate_limited" || ae.Code == "authentication") {
						return e
					}
					// One unavailable optional endpoint does not invalidate verified prices.
					result.Warnings = append(result.Warnings, "Flight-detail retrieval stopped after an unavailable response; aircraft may be unknown.")
					return nil
				}
				d = parseDetail(body)
				cache[endpoint] = d
			}
			result.Itineraries[i].Segments[j].Aircraft = d.aircraft
			result.Itineraries[i].Segments[j].OperatingAirline = d.operator
			unavailable = unavailable || d.aircraft == nil
		}
	}
	if unavailable {
		result.Warnings = append(result.Warnings, "Aircraft was not exposed for every segment; unknown values are null.")
	}
	return nil
}
