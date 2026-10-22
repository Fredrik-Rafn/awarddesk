package ba

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	http "github.com/bogdanfinn/fhttp"
	"golang.org/x/net/html"

	"awarddesk/internal/award"
)

const clientID = "C8jRt63pKXhBXI9WgSUQ3ti9xANkFtph"
const redirect = "https://www.britishairways.com/nx/b/account/en/gbr/account/pre-login/"

type flow struct {
	// Page and Form come from the same fresh response; do not reload the
	// login page independently while preparing collector fields.
	Page                                      []byte
	Verifier, State, Nonce, LoginURL, Sitekey string
	Form                                      url.Values
}
type tokens struct {
	Access    string `json:"access_token"`
	ID        string `json:"id_token"`
	Refresh   string `json:"refresh_token"`
	ExpiresIn int    `json:"expires_in"`
}

func randomString() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func uuid() (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func parseLogin(b []byte) (url.Values, string, error) {
	d, e := html.Parse(bytes.NewReader(b))
	if e != nil {
		return nil, "", e
	}
	v := url.Values{}
	key := ""
	walk(d, func(n *html.Node) {
		if attr(n, "data-captcha-provider") == "hcaptcha" {
			key = attr(n, "data-captcha-sitekey")
		}
		if n.Data == "input" && attr(n, "type") == "hidden" && attr(n, "name") != "" {
			v.Set(attr(n, "name"), attr(n, "value"))
		}
	})
	if key == "" || v.Get("state") == "" {
		return nil, "", award.Failure("unexpected_login", "BA returned an unexpected login page")
	}
	return v, key, nil
}
func (r *runner) prepare() error {
	var e error
	r.flow.Verifier, e = randomString()
	if e != nil {
		return e
	}
	r.flow.State, e = randomString()
	if e != nil {
		return e
	}
	r.flow.Nonce, e = randomString()
	if e != nil {
		return e
	}
	hash := sha256.Sum256([]byte(r.flow.Verifier))
	q := url.Values{"client_id": {clientID}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {"openid profile email read:transaction offline_access"}, "audience": {"https://api.avios.com/"}, "state": {r.flow.State}, "nonce": {r.flow.Nonce}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}}
	raw := "https://accounts.britishairways.com/authorize?" + q.Encode()
	for i := 0; i < 5; i++ {
		res, b, e := r.request("GET", raw, nil, http.Header{"referer": {"https://www.britishairways.com/"}, "sec-fetch-site": {"same-site"}})
		if e != nil {
			return e
		}
		if res.StatusCode >= 300 && res.StatusCode < 400 {
			u, e := res.Location()
			if e != nil {
				return award.Failure("redirect", "missing login redirect")
			}
			raw = u.String()
			continue
		}
		r.flow.Form, r.flow.Sitekey, e = parseLogin(b)
		r.flow.LoginURL = raw
		r.flow.Page = b
		return e
	}
	return errors.New("login redirect limit reached")
}
func oauthHeaders() http.Header {
	return http.Header{"content-type": {"application/json"}, "accept": {"application/json"}, "origin": {"https://www.britishairways.com"}, "sec-fetch-mode": {"cors"}, "sec-fetch-site": {"same-site"}, "sec-fetch-dest": {"empty"}}
}
func (r *runner) login() error {
	r.progress("captcha", "Waiting for hCaptcha verification")
	solution, e := r.config.Captcha.Solve(r.ctx, r.flow.LoginURL, r.flow.Sitekey)
	if e != nil {
		return e
	}
	v := r.flow.Form
	v.Set("username", r.config.Email)
	v.Set("password", r.config.Password)
	for _, k := range []string{"captcha", "h-captcha-response", "g-recaptcha-response"} {
		v.Set(k, solution.Token)
	}
	r.progress("login_submit", "Submitting BA login verification")
	res, rejection, e := r.request("POST", r.flow.LoginURL, strings.NewReader(v.Encode()), http.Header{"content-type": {"application/x-www-form-urlencoded"}, "origin": {"https://accounts.britishairways.com"}, "referer": {r.flow.LoginURL}, "sec-fetch-site": {"same-origin"}})
	v.Del("password")
	v.Del("username")
	if e != nil {
		if res != nil && res.StatusCode == 400 {
			return classifyLoginRejection(rejection, res.StatusCode)
		}
		return e
	}
	if res.StatusCode != 302 {
		return award.Failure("login_rejected", "BA did not accept the login or requires additional verification")
	}
	next, e := res.Location()
	if e != nil {
		return errors.New("missing authentication redirect")
	}
	code := ""
	for i := 0; i < 5; i++ {
		if next.Scheme+"://"+next.Host+next.Path == redirect {
			if next.Query().Get("state") != r.flow.State {
				return errors.New("OAuth state verification failed")
			}
			code = next.Query().Get("code")
			break
		}
		res, _, e = r.request("GET", next.String(), nil, nil)
		if e != nil {
			return e
		}
		if res.StatusCode != 302 {
			return award.Failure("verification_required", "BA requires additional account verification")
		}
		next, e = res.Location()
		if e != nil {
			return errors.New("missing authentication redirect")
		}
	}
	if code == "" {
		return errors.New("authorization code missing")
	}
	p := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "redirect_uri": {redirect}, "code_verifier": {r.flow.Verifier}, "code": {code}}
	h := oauthHeaders()
	h["content-type"] = []string{"application/x-www-form-urlencoded"}
	_, b, e := r.request("POST", "https://accounts.britishairways.com/oauth/token", strings.NewReader(p.Encode()), h)
	if e != nil {
		return e
	}
	if json.Unmarshal(b, &r.tokens) != nil || r.tokens.Access == "" || r.tokens.Refresh == "" {
		return errors.New("invalid OAuth token response")
	}
	h = oauthHeaders()
	h["authorization"] = []string{"Bearer " + r.tokens.Access}
	_, b, e = r.request("GET", "https://accounts.britishairways.com/userinfo", nil, h)
	if e != nil {
		return e
	}
	var info struct {
		Sub string `json:"sub"`
	}
	if json.Unmarshal(b, &info) != nil || info.Sub == "" {
		return errors.New("account verification failed")
	}
	return nil
}
func (r *runner) finalize() error {
	b, _ := json.Marshal(map[string]string{"grant_type": "refresh_token", "client_id": clientID, "refresh_token": r.tokens.Refresh})
	_, b, e := r.request("POST", "https://accounts.britishairways.com/oauth/token", bytes.NewReader(b), oauthHeaders())
	if e != nil {
		return e
	}
	var fresh tokens
	if json.Unmarshal(b, &fresh) != nil || fresh.Access == "" || fresh.ID == "" {
		return errors.New("invalid OAuth refresh response")
	}
	if fresh.Refresh == "" {
		fresh.Refresh = r.tokens.Refresh
	}
	r.tokens = fresh
	_, page, e := r.request("GET", redirect, nil, nil)
	if e != nil {
		return e
	}
	r.progress("challenge", "Preparing BA security checks")
	if e = r.config.Challenge.Prepare(r.ctx, r.client, redirect, page); e != nil {
		return e
	}
	h := http.Header{"accept": {"*/*"}, "content-type": {"application/json"}, "origin": {"https://www.britishairways.com"}, "referer": {redirect}, "sec-fetch-site": {"same-origin"}, "sec-fetch-mode": {"cors"}, "sec-fetch-dest": {"empty"}, "x-ba-application-name": {"nexus"}, "x-ba-channel": {"WEB"}, "x-ba-client-name": {"nexus"}, "x-ba-language": {"en"}, "x-ba-market": {"gb"}, "x-ba-action-name": {"getclassictoken"}, "x-amzn-waf-ba-rule": {"EMPTY"}}
	for _, k := range []string{"x-ba-session-id", "x-ba-device-id", "x-ba-interaction-id", "x-ba-request-id", "x-ba-track-id", "x-ba-user-anon-id"} {
		id, e := uuid()
		if e != nil {
			return e
		}
		h[k] = []string{id}
	}
	res, b, e := r.request("GET", "https://www.britishairways.com/nx/b/bff/customer-account/v0/csrf", nil, h)
	if e != nil {
		return e
	}
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") || len(bytes.TrimSpace(b)) == 0 {
		return award.Failure("csrf", "BA did not return the expected CSRF token")
	}
	h["x-xsrf-token"] = []string{strings.TrimSpace(string(b))}
	h["authorization"] = []string{"Bearer " + r.tokens.Access}
	h["accept"] = []string{"application/json, text/plain, */*"}
	h["referer"] = []string{"https://www.britishairways.com/nx/b/account/en/gbr/account/auth-callback/"}
	b, _ = json.Marshal(map[string]string{"access_token": r.tokens.Access, "id_token": r.tokens.ID})
	_, b, e = r.request("POST", "https://www.britishairways.com/nx/b/bff/customer-account/v0/getClassicTokens", bytes.NewReader(b), h)
	if e != nil {
		return e
	}
	if json.Unmarshal(b, &r.classic) != nil || r.classic.Access == "" {
		return award.Failure("classic_session", "BA did not return booking-session tokens")
	}
	return nil
}
