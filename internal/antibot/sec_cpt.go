package antibot

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/Hyper-Solutions/hyper-sdk-go/v3/akamai"
	http "github.com/bogdanfinn/fhttp"
	"golang.org/x/net/html"

	"awarddesk/internal/award"
	"awarddesk/internal/session"
)

var challengeDuration = regexp.MustCompile(`data-duration=(\d+)`)

func (h Hyper) PrepareAdaptive(ctx context.Context, c *session.Client, page string, body []byte) error {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return err
	}
	var challengeEncoded, branding, provider string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "iframe" {
			a := map[string]string{}
			for _, v := range n.Attr {
				a[v.Key] = v.Val
			}
			if a["id"] == "sec-cpt-if" {
				challengeEncoded = a["challenge"]
				provider = a["provider"]
				branding = a["src"]
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	raw, err := base64.StdEncoding.DecodeString(challengeEncoded)
	var limits struct{ Difficulty, Count int }
	durationMatch := challengeDuration.FindSubmatch(body)
	if err != nil || json.Unmarshal(raw, &limits) != nil || provider != "adaptive" || limits.Count < 1 || limits.Count > 4 || limits.Difficulty < 1 || limits.Difficulty > 100000 || len(durationMatch) != 2 {
		return award.Failure("challenge_changed", "EVA requested an unsupported security challenge")
	}
	duration, _ := strconv.Atoi(string(durationMatch[1]))
	if duration > 60 {
		return award.Failure("challenge_limit", "EVA security wait exceeds the supported limit")
	}
	challenge, err := akamai.ParseSecCptChallenge(bytes.NewReader(body))
	if err != nil {
		return award.Failure("challenge_changed", "EVA security challenge could not be parsed")
	}
	base, _ := url.Parse(page)
	cookieValue := func(name string) string {
		for _, cookie := range c.Cookies(base) {
			if cookie.Name == name {
				return cookie.Value
			}
		}
		return ""
	}
	challenge.SleepWithContext(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	cookie := cookieValue("sec_cpt")
	if cookie == "" {
		return award.Failure("challenge_changed", "EVA security challenge cookie missing")
	}
	payload, err := challenge.GenerateSecCptPayload(cookie)
	if err != nil {
		return award.Failure("challenge_changed", "EVA security proof could not be calculated")
	}
	origin := base.Scheme + "://" + base.Host
	_, _, err = c.Do(ctx, "POST", origin+"/_sec/verify?provider=adaptive", bytes.NewReader(payload), http.Header{"sec-fetch-site": {"same-origin"}, "content-type": {"text/plain;charset=UTF-8"}, "origin": {origin}, "referer": {page}, "sec-fetch-mode": {"cors"}, "sec-fetch-dest": {"empty"}})
	if err != nil {
		return err
	}
	brandingURL, _, err := challengeEndpoint(page, branding)
	if err != nil {
		return err
	}
	_, brandingBody, err := c.Do(ctx, "GET", brandingURL, nil, http.Header{"sec-fetch-site": {"same-origin"}, "referer": {page}, "sec-fetch-dest": {"iframe"}})
	if err != nil {
		return err
	}
	// Some deployments place the sensor on the interstitial rather than
	// the challenge iframe. Never use the explanatory sec-text-if frame.
	if _, parseErr := parseSensorPath(brandingBody); parseErr != nil {
		brandingURL, brandingBody = page, body
	}
	// Adaptive requires fresh sensor context after its proof-of-work step.
	if err = (Hyper{Key: h.Key, VerifyApplication: true}).Prepare(ctx, c, brandingURL, brandingBody); err != nil {
		return err
	}
	_, verified, err := c.Do(ctx, "GET", origin+"/_sec/cp_challenge/verify", nil, http.Header{"sec-fetch-site": {"same-origin"}, "referer": {page}, "sec-fetch-mode": {"cors"}, "sec-fetch-dest": {"empty"}})
	if err != nil {
		return err
	}
	var status struct{ Success any }
	if json.Unmarshal(verified, &status) != nil || (status.Success != "true" && status.Success != true) || !strings.Contains(cookieValue("sec_cpt"), "~3~") {
		return award.Failure("challenge_rejected", "EVA adaptive security verification failed")
	}
	return nil
}
