package eva

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

func awardText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "input") {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func awardAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func awardClass(n *html.Node, class string) bool {
	return strings.Contains(" "+awardAttr(n, "class")+" ", " "+class+" ")
}

func awardNodes(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var found []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if match(n) {
			found = append(found, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return found
}

func awardDocument(body []byte) (*html.Node, error) {
	return html.Parse(bytes.NewReader(body))
}

var awardDisplayDate = regexp.MustCompile(`\b(?:(\d{1,2})\s+([A-Za-z]{3})\.\s*(\d{4})|([A-Za-z]{3})\.\s+(\d{1,2}),\s*(\d{4}))(?:\s+(\d{2}:\d{2}))?`)
var awardFlightNumber = regexp.MustCompile(`\b(?:BR|B7)\s*\d{1,4}\b`)
var awardFlightParts = regexp.MustCompile(`^(BR|B7)\s*([0-9]{1,4})$`)
var awardAirportCode = regexp.MustCompile(`\b[A-Z]{3}\b`)

func parseAwardDate(text string, clock bool) (string, bool) {
	m := awardDisplayDate.FindStringSubmatch(text)
	if len(m) != 8 || (clock && m[7] == "") {
		return "", false
	}
	if m[1] == "" {
		m[1], m[2], m[3] = m[5], m[4], m[6]
	}
	value, layout, output := m[1]+" "+m[2]+" "+m[3], "2 Jan 2006", "2006-01-02"
	if clock {
		value, layout, output = value+" "+m[7], layout+" 15:04", output+"T15:04:05"
	}
	d, err := time.Parse(layout, value)
	return d.Format(output), err == nil
}

func canonicalAwardFlight(value string) string {
	m := awardFlightParts.FindStringSubmatch(strings.TrimSpace(value))
	if len(m) != 3 {
		return ""
	}
	n, err := strconv.Atoi(m[2])
	if err != nil || n <= 0 {
		return ""
	}
	return m[1] + strconv.Itoa(n)
}

func awardFlightKey(segments []string) string {
	keys := make([]string, len(segments))
	for i, s := range segments {
		keys[i] = canonicalAwardFlight(s)
	}
	return strings.Join(keys, "/")
}
