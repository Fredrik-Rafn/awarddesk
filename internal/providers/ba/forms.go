package ba

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	http "github.com/bogdanfinn/fhttp"
	"golang.org/x/net/html"
)

const awardBase = "https://www.britishairways.com/travel/redeem/execclub/_gf/en_dk"

func hiddenFields(body []byte, formID string) (url.Values, error) {
	doc, e := html.Parse(bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	root := doc
	if formID != "" {
		root = first(doc, func(n *html.Node) bool {
			return n.Data == "form" && (attr(n, "id") == formID || attr(n, "name") == formID)
		})
		if root == nil {
			return nil, fmt.Errorf("expected form %s missing", formID)
		}
	}
	v := url.Values{}
	walk(root, func(n *html.Node) {
		if n.Data == "input" && attr(n, "type") == "hidden" && attr(n, "name") != "" {
			v.Set(attr(n, "name"), attr(n, "value"))
		}
	})
	return v, nil
}
func continuationFields(body []byte) (url.Values, error) {
	v, e := hiddenFields(body, "SubmitFromInterstitial")
	if e != nil {
		return nil, e
	}
	m := regexp.MustCompile(`var\s+eventId\s*=\s*['"]([0-9]+)['"]`).FindSubmatch(body)
	if len(m) != 2 || string(m[1]) != "111011" {
		return nil, errors.New("unexpected search continuation event")
	}
	v.Set("eId", string(m[1]))
	return v, nil
}
func (r *runner) navigate(raw string) ([]byte, error) {
	for i := 0; i < 6; i++ {
		resp, b, e := r.request("GET", raw, nil, nil)
		if e != nil {
			return nil, e
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			u, e := resp.Location()
			if e != nil {
				return nil, e
			}
			raw = u.String()
			continue
		}
		return b, nil
	}
	return nil, errors.New("navigation redirect limit")
}
func (r *runner) postAward(raw string, v url.Values) ([]byte, error) {
	_, b, e := r.request("POST", raw, strings.NewReader(v.Encode()), http.Header{"content-type": {"application/x-www-form-urlencoded"}, "origin": {"https://www.britishairways.com"}, "referer": {awardBase}, "sec-fetch-site": {"same-origin"}})
	return b, e
}
