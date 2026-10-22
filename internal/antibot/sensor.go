package antibot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"path"
	"strings"
	"time"

	hyper "github.com/Hyper-Solutions/hyper-sdk-go/v3"
	"github.com/Hyper-Solutions/hyper-sdk-go/v3/akamai"
	http "github.com/bogdanfinn/fhttp"
	"golang.org/x/net/html"

	"awarddesk/internal/award"
)

// Optional, bounded sensor initialization using fresh scripts and this runner's jar.
func (r *hyperSession) initializeSensor(page string, body []byte) error {
	key := r.key
	if key == "" {
		return nil
	}
	resolve := func(body []byte) (string, error) {
		return resolveSensorScript(body, func(body []byte) ([]byte, error) {
			log.Print("SBSD challenge detected; applying bounded challenge flow")
			return r.solveSbsd(page, body)
		})
	}
	path, e := resolve(body)
	if e != nil {
		var upstream *award.Error
		if errors.As(e, &upstream) || errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
			return e
		}
		return award.Failure("challenge_changed", "Airline security checks changed or returned an unrecognized page")
	}
	base, _ := url.Parse(page)
	endpoint, origin, e := challengeEndpoint(page, path)
	if e != nil {
		return e
	}
	_, script, e := r.request("GET", endpoint, nil, http.Header{"referer": {page}, "sec-fetch-mode": {"no-cors"}, "sec-fetch-dest": {"script"}, "sec-fetch-site": {"same-origin"}})
	if e != nil {
		return e
	}
	ipValue, e := r.outboundIP()
	if e != nil {
		return e
	}
	api := hyper.NewSession(key)
	ctx, cancel := context.WithTimeout(r.ctx, 90*time.Second)
	defer cancel()
	sensorContext := ""
	for i := 0; i < 3; i++ {
		cookies := map[string]string{}
		for _, c := range r.client.Cookies(base) {
			cookies[c.Name] = c.Value
		}
		if cookies["_abck"] == "" || cookies["bm_sz"] == "" {
			return errors.New("fresh Akamai cookies absent")
		}
		input := &hyper.SensorInput{Abck: cookies["_abck"], Bmsz: cookies["bm_sz"], Version: "3", PageUrl: page, UserAgent: r.client.UserAgent(), ScriptUrl: endpoint, AcceptLanguage: lang, IP: ipValue, Context: sensorContext}
		if i == 0 {
			input.Script = string(script)
		}
		data, next, e := api.GenerateSensorData(ctx, input)
		if e != nil {
			for _, reason := range []string{"access denied", "domain not whitelisted", "insufficient balance", "invalid script", "unsupported version", "invalid api key", "missing script", "failed to parse script", "not supported", "unauthorized", "rate limit", "invalid_api_key", "insufficient_balance", "insufficient credits", "script", "decode", "unmarshal", "status code", "bad request", "version", "eof", "context deadline", "connection", "unsupported", "failed to generate"} {
				if strings.Contains(strings.ToLower(e.Error()), reason) {
					return award.Failure("challenge_service", "Hyper security generation failed: "+reason)
				}
			}
			return award.Failure("challenge_service", "Hyper could not generate the security response; check its service status and key configuration")
		}
		sensorContext = next
		payload, _ := json.Marshal(map[string]string{"sensor_data": data})
		response, _, e := r.request("POST", endpoint, bytes.NewReader(payload), http.Header{"content-type": {"text/plain;charset=UTF-8"}, "accept": {"*/*"}, "origin": {origin}, "referer": {page}, "sec-fetch-mode": {"cors"}, "sec-fetch-dest": {"empty"}, "sec-fetch-site": {"same-origin"}})
		if e != nil {
			return e
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return award.Failure("challenge_rejected", "Airline did not accept the security sensor submission")
		}
		log.Printf("Sensor submission %d completed", i+1)
		for _, c := range r.client.Cookies(base) {
			if c.Name == "_abck" && sensorMayStop(c.Value, i+1, r.minimumSensorRounds) {
				log.Print("Akamai cookie validation marker observed; application access still unverified")
				return nil
			}
		}
	}
	if r.verifyApplication {
		log.Print("Sensor rounds completed; authenticated application verification is required")
		return nil
	}
	return award.Failure("challenge_rejected", "Airline security verification did not complete within the request limit")
}

func sensorMayStop(cookie string, submitted, minimum int) bool {
	return submitted >= minimum && akamai.IsCookieValid(cookie, submitted)
}

// Security scripts and submissions must stay on the page's own HTTPS origin.
// Relative scripts resolve against the current EVA page.
func challengeEndpoint(page, path string) (endpoint, origin string, err error) {
	base, err := url.Parse(page)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil {
		return "", "", award.Failure("unsafe_destination", "Invalid security page origin")
	}
	relative, err := url.Parse(path)
	if err != nil {
		return "", "", award.Failure("unsafe_destination", "Invalid security script URL")
	}
	target := base.ResolveReference(relative)
	if target.Scheme != "https" || target.Host != base.Host || target.User != nil || target.Fragment != "" {
		return "", "", award.Failure("unsafe_destination", "Security endpoint is outside the page origin")
	}
	return target.String(), base.Scheme + "://" + base.Host, nil
}

// SBSD and the regular sensor can coexist. Finding the regular script must not
// skip SBSD. Resolve once, then parse the refreshed page without recursion.
func resolveSensorScript(body []byte, solve func([]byte) ([]byte, error)) (string, error) {
	if sbsdPattern.Match(body) {
		var err error
		body, err = solve(body)
		if err != nil {
			return "", err
		}
	}
	return parseSensorPath(body)
}

// The SDK's regular expression requires a particular script attribute order
// and does not recognize EVA's adaptive interstitial's async/defer tag.
func parseSensorPath(body []byte) (string, error) {
	if value, err := akamai.ParseScriptPath(bytes.NewReader(body)); err == nil {
		return value, nil
	}
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	paths := map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "script" {
			for _, a := range n.Attr {
				if a.Key == "src" {
					u, e := url.Parse(a.Val)
					if e == nil && u.Host == "" && u.Scheme == "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && strings.HasPrefix(u.Path, "/") && strings.Count(u.Path, "/") >= 3 && path.Ext(u.Path) == "" {
						paths[u.Path] = true
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if len(paths) != 1 {
		return "", akamai.ErrScriptPathNotFound
	}
	for value := range paths {
		return value, nil
	}
	return "", akamai.ErrScriptPathNotFound
}
