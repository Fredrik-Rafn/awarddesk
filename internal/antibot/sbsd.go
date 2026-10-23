package antibot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"regexp"
	"time"

	hyper "github.com/Hyper-Solutions/hyper-sdk-go/v3"
	http "github.com/bogdanfinn/fhttp"

	"awarddesk/internal/award"
)

var sbsdPattern = regexp.MustCompile(`(?i)([a-z\d/\-_\.]+)\?v=([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})(?:&[^"']*?\bt=([^"'&]+))?[^"']*["']`)

func (r *hyperSession) solveSbsd(page string, body []byte) ([]byte, error) {
	m := sbsdPattern.FindSubmatch(body)
	if len(m) < 3 {
		return nil, errors.New("no supported SBSD challenge in unexpected page")
	}
	base, e := url.Parse(page)
	if e != nil {
		return nil, e
	}
	endpoint, origin, e := challengeEndpoint(page, string(m[1]))
	if e != nil {
		return nil, e
	}
	target, _ := url.Parse(endpoint)
	q := url.Values{"v": {string(m[2])}}
	hard := len(m) > 3 && len(m[3]) > 0
	if hard {
		q.Set("t", string(m[3]))
	}
	target.RawQuery = q.Encode()
	_, script, e := r.request("GET", target.String(), nil, http.Header{"referer": {page}, "sec-fetch-mode": {"no-cors"}, "sec-fetch-dest": {"script"}, "sec-fetch-site": {"same-origin"}})
	if e != nil {
		return nil, e
	}
	key := r.key
	ipValue, e := r.outboundIP()
	if e != nil {
		return nil, e
	}
	api := hyper.NewSession(key)
	ctx, cancel := context.WithTimeout(r.ctx, 90*time.Second)
	defer cancel()
	count := 2
	if hard {
		count = 1
	}
	q.Del("v")
	target.RawQuery = q.Encode()
	for i := 0; i < count; i++ {
		o := ""
		for _, c := range r.client.Cookies(base) {
			if c.Name == "bm_so" || c.Name == "sbsd_o" {
				o = c.Value
			}
		}
		if o == "" {
			return nil, errors.New("SBSD O cookie missing")
		}

		in := &hyper.SbsdInput{Index: i, UserAgent: r.client.UserAgent(), Uuid: string(m[2]), PageUrl: page, OCookie: o, AcceptLanguage: lang, IP: ipValue, Script: string(script)}
		payload, _, e := api.GenerateSbsdData(ctx, in)
		if e != nil {
			return nil, award.Failure("challenge_service", "Hyper could not generate the SBSD response")
		}
		data, _ := json.Marshal(map[string]string{"body": payload})
		response, _, e := r.request("POST", target.String(), bytes.NewReader(data), http.Header{"content-type": {"application/json"}, "accept": {"*/*"}, "origin": {origin}, "referer": {page}, "sec-fetch-mode": {"cors"}, "sec-fetch-dest": {"empty"}, "sec-fetch-site": {"same-origin"}})
		if e != nil {
			return nil, e
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, award.Failure("challenge_rejected", "Airline did not accept the SBSD submission")
		}
		log.Printf("SBSD submission %d completed", i+1)
	}
	_, result, e := r.request("GET", page, nil, nil)
	return result, e
}
