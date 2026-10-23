package antibot

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"strings"
	"time"

	hyper "github.com/Hyper-Solutions/hyper-sdk-go/v3"
	"github.com/Hyper-Solutions/hyper-sdk-go/v3/incapsula"
	http "github.com/bogdanfinn/fhttp"

	"awarddesk/internal/award"
	"awarddesk/internal/session"
)

func (h Hyper) PrepareReese(ctx context.Context, c *session.Client, page string, body []byte) (resultErr error) {
	started := time.Now()
	defer func() {
		slog.Info("EVA Reese84 verification", "solver", "hyper", "success", resultErr == nil, "elapsedMs", time.Since(started).Milliseconds())
	}()
	sensorPath, scriptPath, err := incapsula.ParseDynamicReeseScript(bytes.NewReader(body), page)
	if err != nil {
		return award.Failure("challenge_changed", "EVA booking security page was not recognized")
	}
	scriptURL, _, err := challengeEndpoint(page, scriptPath)
	if err != nil {
		return err
	}
	sensorURL, origin, err := challengeEndpoint(page, sensorPath)
	if err != nil {
		return err
	}
	r := hyperSession{ctx: ctx, client: c, key: h.Key}
	_, script, err := r.request("GET", scriptURL, nil, http.Header{"sec-fetch-site": {"same-origin"}, "referer": {page}, "sec-fetch-mode": {"no-cors"}, "sec-fetch-dest": {"script"}})
	if err != nil {
		return err
	}
	ip, err := r.outboundIP()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	payload, err := hyper.NewSession(h.Key).GenerateReese84Sensor(ctx, &hyper.ReeseInput{UserAgent: c.UserAgent(), AcceptLanguage: session.Language, IP: ip, ScriptUrl: scriptURL, PageUrl: page, Script: string(script)})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "access denied") {
			return award.Failure("challenge_service", "Hyper denied Reese84 API access; enable Imperva/Reese84 for this key and book.evaair.com")
		}
		if strings.Contains(strings.ToLower(err.Error()), "domain not whitelisted") {
			return award.Failure("challenge_service", "Hyper requires book.evaair.com to be whitelisted")
		}
		for _, reason := range []string{"failed to generate", "failed to parse", "unsupported", "invalid script", "insufficient balance", "insufficient credits", "unauthorized", "not enabled", "not whitelisted", "invalid api key", "timeout", "status code 403", "status code 400", "status code 500"} {
			if strings.Contains(strings.ToLower(err.Error()), reason) {
				return award.Failure("challenge_service", "Hyper booking security generation failed: "+reason)
			}
		}
		return award.Failure("challenge_service", "Hyper could not generate EVA booking security response")
	}
	return submitReese(ctx, c, page, sensorURL, origin, payload, session.Language)
}

type reeseClient interface {
	challengeClient
	SetCookies(*url.URL, []*http.Cookie)
}

func submitReese(ctx context.Context, c reeseClient, page, sensorURL, origin, payload, language string) error {
	_, data, err := c.Do(ctx, "POST", sensorURL, strings.NewReader(payload), http.Header{"sec-fetch-site": {"same-origin"}, "content-type": {"text/plain;charset=UTF-8"}, "accept": {"application/json; charset=utf-8"}, "accept-language": {language}, "referer": {page}, "origin": {origin}, "sec-fetch-mode": {"cors"}, "sec-fetch-dest": {"empty"}})
	if err != nil {
		return err
	}
	var response struct {
		Token        string
		CookieDomain string
		RenewInSec   int
	}
	if json.Unmarshal(data, &response) != nil || response.Token == "" {
		return award.Failure("challenge_rejected", "EVA booking security did not return a token")
	}
	u, _ := url.Parse(page)
	domain := strings.TrimPrefix(response.CookieDomain, ".")
	if domain != "" && domain != u.Hostname() && !(domain == "evaair.com" && strings.HasSuffix(u.Hostname(), ".evaair.com")) {
		return award.Failure("challenge_changed", "EVA booking security returned a different cookie domain")
	}

	c.SetCookies(u, []*http.Cookie{{Name: "reese84", Path: "/", MaxAge: -1}})
	if strings.HasSuffix(u.Hostname(), ".evaair.com") {
		c.SetCookies(u, []*http.Cookie{{Name: "reese84", Domain: "evaair.com", Path: "/", MaxAge: -1}})
	}
	token := &http.Cookie{Name: "reese84", Value: response.Token, Domain: domain, Path: "/", Secure: true}
	if response.RenewInSec > 0 {
		token.Expires = time.Now().Add(time.Duration(response.RenewInSec) * time.Second)
	}
	c.SetCookies(u, []*http.Cookie{token})
	return nil
}
