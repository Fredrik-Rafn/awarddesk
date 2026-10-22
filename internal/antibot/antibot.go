// Package antibot supplies independent CAPTCHA and browser-challenge adapters.
package antibot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"

	http "github.com/bogdanfinn/fhttp"

	"awarddesk/internal/award"
	"awarddesk/internal/session"
)

type Captcha interface {
	Solve(context.Context, string, string) (CaptchaSolution, error)
}

// CaptchaSolution preserves the solver's actual browser identity. A requested
// user-agent is a hint to the upstream provider, not a guarantee.
type CaptchaSolution struct {
	Token     string
	UserAgent string
}
type Hyper struct {
	Key string
	// VerifyApplication permits completed sensor rounds without a stop marker.
	// The caller must then verify access through EVA's application response.
	VerifyApplication bool
	// MinimumSensorRounds prevents an inherited stop marker from ending a new
	// protected-action sequence before its interaction sensors are submitted.
	MinimumSensorRounds int
}
type challengeClient interface {
	Do(context.Context, string, string, io.Reader, http.Header) (*http.Response, []byte, error)
	Cookies(*url.URL) []*http.Cookie
	UserAgent() string
}
type hyperSession struct {
	ctx                 context.Context
	client              challengeClient
	key                 string
	verifyApplication   bool
	minimumSensorRounds int
	ip                  string
}

const lang = session.Language

func (h Hyper) Prepare(ctx context.Context, c *session.Client, page string, body []byte) error {
	if h.Key == "" {
		return errors.New("Hyper API key is not configured")
	}
	if h.MinimumSensorRounds < 0 || h.MinimumSensorRounds > 3 {
		return errors.New("invalid sensor round limit")
	}
	r := hyperSession{ctx: ctx, client: c, key: h.Key, verifyApplication: h.VerifyApplication, minimumSensorRounds: h.MinimumSensorRounds}
	return r.initializeSensor(page, body)
}

// VerifyConnection checks the same locked proxy before account submission.
func (h Hyper) VerifyConnection(ctx context.Context, c *session.Client) error {
	r := hyperSession{ctx: ctx, client: c, key: h.Key}
	_, err := r.outboundIP()
	return err
}
func (r *hyperSession) request(method, raw string, body io.Reader, h http.Header) (*http.Response, []byte, error) {
	return r.client.Do(r.ctx, method, raw, body, h)
}
func (r *hyperSession) outboundIP() (string, error) {
	if err := r.ctx.Err(); err != nil {
		return "", err
	}
	if r.ip != "" {
		return r.ip, nil
	}
	res, b, e := r.request("GET", "https://api.ipify.org?format=json", nil, http.Header{"accept": {"application/json"}})
	if e != nil {
		if res != nil && res.StatusCode >= 400 {
			return "", &award.Error{Code: "proxy_verification", Message: "Proxy IP verification service returned HTTP " + strconv.Itoa(res.StatusCode) + "; airline authentication was not checked", HTTPStatus: res.StatusCode}
		}
		return "", e
	}
	var ip struct {
		IP string `json:"ip"`
	}
	if json.Unmarshal(b, &ip) != nil || net.ParseIP(ip.IP) == nil {
		return "", errors.New("invalid outbound IP response")
	}
	r.ip = ip.IP
	if client, ok := r.client.(interface{ PinEgressIP(string) error }); ok {
		if err := client.PinEgressIP(r.ip); err != nil {
			return "", err
		}
	}
	return r.ip, nil
}
