// Package ana implements ANA Mileage Club login. Award search is not implemented.
package ana

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	http "github.com/bogdanfinn/fhttp"

	"awarddesk/internal/antibot"
	"awarddesk/internal/award"
	"awarddesk/internal/proxies"
	"awarddesk/internal/session"
)

const home = "https://www.ana.co.jp/en/eur/"
const api = "https://space.ana.co.jp"

type Config struct {
	MemberNumber string
	Password     string
	Proxy        string
	Challenge    antibot.Challenge
	Pool         *proxies.Pool
}

type Provider struct {
	config Config
	gate   chan struct{}
}

func New(config Config) *Provider { return &Provider{config: config, gate: make(chan struct{}, 1)} }

// LoginSession owns a fresh authenticated cookie jar. It contains no exported
// credentials or tokens, must not be used concurrently, and must be closed.
type LoginSession struct {
	client        *session.Client
	token         string
	release       func(bool)
	once          sync.Once
	clientID      string
	clientKey     string
	correlation   string
	proxy         string
	proxyTemplate string
	createdAt     time.Time
}

func (s *LoginSession) Close() {
	s.once.Do(func() {
		s.client.Close()
		s.token = ""
		if s.release != nil {
			s.release(false)
		}
	})
}

type transport interface {
	Do(context.Context, string, string, io.Reader, http.Header) (*http.Response, []byte, error)
}

type loginFlow struct {
	client               transport
	config               Config
	report               award.Reporter
	clientID             string
	clientKey            string
	token                string
	correlation          string
	bootstrapped         bool
	stage                string
	credentialsSubmitted bool
	verifyConnection     func(context.Context) error
}

// Login performs authorization, one credential submission, and an authenticated
// member check. It never reads HAR files or replays captured session material.
func (p *Provider) Login(ctx context.Context, report award.Reporter) (*LoginSession, error) {
	if err := validate(p.config); err != nil {
		return nil, err
	}
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	default:
		return nil, award.Failure("busy", "An ANA login for this account is already running")
	}
	ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		proxy := p.config.Proxy
		var lease *proxies.Lease
		if p.config.Pool != nil {
			var err error
			lease, err = p.config.Pool.Acquire(ctx)
			if err != nil {
				return nil, award.Failure("proxy_pool", err.Error())
			}
			proxy = lease.URL
		}
		s, err := p.loginOnce(ctx, proxy, report)
		if err == nil {
			if lease != nil {
				s.release = lease.Release
				s.proxyTemplate = lease.Template
			}
			return s, nil
		}
		var ae *award.Error
		errors.As(err, &ae)
		retry := retryBeforeCredentials(err)
		if lease != nil {
			lease.Release(retry || ae != nil && (ae.Code == "connection" || ae.Code == "response_read"))
		}
		if !retry || attempt == 1 || ctx.Err() != nil {
			return nil, err
		}
		if report != nil {
			report(award.Progress{Stage: "retry", Message: "Connection failed before credentials; starting one fresh session"})
		}
	}
	return nil, award.Failure("connection", "ANA login could not complete")
}

func retryBeforeCredentials(err error) bool {
	var ae *award.Error
	return errors.As(err, &ae) && !ae.OutcomeUnknown && ae.Stage != "credentials" && ae.Stage != "member verification" &&
		(ae.Code == "connection" || ae.Code == "response_read" || ae.Code == "provider_http" && ae.HTTPStatus >= 500)
}

func (p *Provider) loginOnce(ctx context.Context, proxy string, report award.Reporter) (*LoginSession, error) {
	client, err := session.New(proxy, []string{"www.ana.co.jp", "space.ana.co.jp", "api.ipify.org"})
	if err != nil {
		return nil, err
	}
	f := loginFlow{client: client, config: p.config, report: report, stage: "security initialization"}
	if proxy != "" {
		if verifier, ok := p.config.Challenge.(interface {
			VerifyConnection(context.Context, *session.Client) error
		}); ok {
			f.verifyConnection = func(ctx context.Context) error { return verifier.VerifyConnection(ctx, client) }
		}
	}
	if p.config.Challenge != nil {
		f.progress("Initializing ANA browser security checks")
		res, body, err := client.Do(ctx, "GET", home, nil, nil)
		if err == nil && res.StatusCode != 200 {
			err = award.Failure("login_http", "ANA homepage returned an unexpected HTTP status")
		}
		if err == nil {
			err = p.config.Challenge.Prepare(ctx, client, home, body)
		}
		if err != nil {
			client.Close()
			return nil, loginFailure(err, f.stage, false)
		}
		f.bootstrapped = true
	}
	if err := f.run(ctx); err != nil {
		client.Close()
		return nil, loginFailure(err, f.stage, f.credentialsSubmitted)
	}
	return &LoginSession{client: client, token: f.token, clientID: f.clientID, clientKey: f.clientKey, correlation: f.correlation, proxy: proxy, createdAt: time.Now()}, nil
}

func loginFailure(err error, stage string, submitted bool) error {
	e := award.Error{Code: "login_failed", Message: "login could not complete", Stage: stage}
	var source *award.Error
	if errors.As(err, &source) {
		e = *source
		e.Stage = stage
	}
	if errors.Is(err, context.DeadlineExceeded) {
		e.Code = "timeout"
		e.Message = "login deadline exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	e.OutcomeUnknown = submitted && (e.Code == "connection" || e.Code == "response_read" || e.Code == "timeout")
	e.Message = "ANA " + stage + ": " + e.Message
	if e.OutcomeUnknown {
		e.Message += ". Login outcome is uncertain; credentials were not submitted again."
	}
	return &e
}

func validate(c Config) error {
	if len(c.MemberNumber) != 10 || strings.Trim(c.MemberNumber, "0123456789") != "" || c.Password == "" {
		return award.Failure("configuration", "Set ANA_MEMBER_NUMBER to the ten-digit AMC number and ANA_PASSWORD to the web password")
	}
	// ANA routes four-digit PIN login into a separate browser flow.
	if len(c.Password) == 4 {
		return award.Failure("configuration", "ANA login requires the web password, not the four-digit AMC PIN")
	}
	return nil
}

func (f *loginFlow) progress(message string) {
	if f.report != nil {
		f.report(award.Progress{Stage: f.stage, Message: message})
	}
}

func (f *loginFlow) request(ctx context.Context, method, target string, payload any, out any) error {
	switch {
	case strings.Contains(target, "sso-authentication"):
		f.stage = "credentials"
		f.credentialsSubmitted = true
	case strings.Contains(target, "/authorization"):
		f.stage = "authorization"
	case strings.Contains(target, "amcmember-decrypt"):
		f.stage = "member verification"
	default:
		f.stage = "session setup"
	}
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return award.Failure("login_protocol", "Could not construct ANA login request")
		}
		body = bytes.NewReader(b)
	}
	h := http.Header{"referer": {home}, "accept": {"application/json"}, "sec-fetch-mode": {"cors"}, "sec-fetch-dest": {"empty"}, "sec-fetch-site": {"same-origin"}}
	if strings.HasPrefix(target, api+"/") {
		h["origin"] = []string{"https://www.ana.co.jp"}
		h["sec-fetch-site"] = []string{"same-site"}
		h["client_id"] = []string{f.clientID}
		h["client_secret"] = []string{f.clientKey}
		h["identification_id"] = []string{home}
		h["sys_id"] = []string{"ASW"}
		if f.token != "" {
			h["authorization"] = []string{"Bearer " + f.token}
		}
		if f.correlation != "" {
			h["x-correlation-id"] = []string{f.correlation}
		}
	}
	if payload != nil {
		h["content-type"] = []string{"application/json"}
	}
	res, b, err := f.client.Do(ctx, method, target, body, h)
	if err != nil {
		var ae *award.Error
		if errors.As(err, &ae) && ae.HTTPStatus == 403 {
			return &award.Error{Code: "access_denied", Message: "ANA denied this session (HTTP 403); no automatic credential retry", HTTPStatus: 403}
		}
		return err
	}
	if res.StatusCode != 200 {
		return award.Failure("login_http", "ANA login returned an unexpected HTTP status")
	}
	if id := res.Header.Get("x-correlation-id"); id != "" {
		f.correlation = id
	}
	if out != nil {
		var envelope struct {
			ErrorCode json.RawMessage `json:"errorCode"`
		}
		if json.Unmarshal(b, &envelope) != nil || (len(envelope.ErrorCode) > 0 && string(envelope.ErrorCode) != "null" && string(envelope.ErrorCode) != "[]") {
			return award.Failure("login_response", "ANA returned an unrecognized or unsuccessful login response")
		}
		if json.Unmarshal(b, out) != nil {
			return award.Failure("login_response", "ANA login response format changed")
		}
	}
	return nil
}

func (f *loginFlow) run(ctx context.Context) error {
	f.progress("Creating a fresh ANA session")
	// The browser loads these public frontend API credentials dynamically.
	// They are not the member password and must not be copied from the HAR.
	var keys struct {
		ID     string `json:"client_id"`
		Secret string `json:"client_secret"`
	}
	if !f.bootstrapped {
		if err := f.request(ctx, "GET", home, nil, nil); err != nil {
			return err
		}
	}
	if err := f.request(ctx, "GET", "https://www.ana.co.jp/api-secret/client.json", nil, &keys); err != nil {
		return err
	}
	if keys.ID == "" || keys.Secret == "" {
		return award.Failure("login_response", "ANA frontend API configuration is missing")
	}
	f.clientID, f.clientKey = keys.ID, keys.Secret
	var auth struct {
		Status string `json:"status"`
		Token  string `json:"access_token"`
		State  string `json:"loginState"`
	}
	f.stage = "authorization"
	f.progress("Requesting ANA authorization")
	if err := f.request(ctx, "POST", api+"/auth/api/v2/authorization", map[string]string{"usableState": "3", "personalUpdateFlag": ""}, &auth); err != nil {
		return err
	}
	if auth.Status != "1" || auth.State != "3" || auth.Token == "" {
		return award.Failure("login_authorization", "ANA did not authorize the fresh login session")
	}
	f.token = auth.Token
	if f.verifyConnection != nil {
		f.stage = "proxy verification"
		f.progress("Checking that the proxy IP has not changed")
		if err := f.verifyConnection(ctx); err != nil {
			return err
		}
	}
	var sso struct {
		Param   string `json:"param"`
		Token   string `json:"access_token"`
		TwoStep string `json:"twoStepAuth"`
	}
	f.stage = "credentials"
	f.progress("Submitting ANA credentials")
	payload := map[string]string{
		"mebrNo": f.config.MemberNumber, "mebrPswd": f.config.Password,
		"persistentFlag": "false", "lngType": "en", "auth": "web",
		"returnUrl": "", "updateFlag": "false",
	}
	if err := f.request(ctx, "POST", api+"/auth/api/v2/sso-authentication", payload, &sso); err != nil {
		return err
	}
	if sso.TwoStep == "true" {
		return award.Failure("mfa_required", "ANA requires two-step verification; complete it on the ANA website")
	}
	if sso.Param != "" {
		return award.Failure("browser_required", "ANA requires an additional browser login step")
	}
	if sso.TwoStep != "false" {
		return award.Failure("login_response", "ANA did not confirm the SSO login state")
	}
	// Successful SSO in the supplied flow sets cookies but returns an empty
	// access_token. Keep the authorization token unless ANA replaces it.
	if sso.Token != "" {
		f.token = sso.Token
	}
	f.stage = "member verification"
	f.progress("Verifying the authenticated ANA member session")
	if err := f.verifyMember(ctx); err != nil {
		return err
	}
	f.progress("ANA login verified")
	return nil
}

func (f *loginFlow) verifyMember(ctx context.Context) error {
	var member struct {
		Personal struct {
			Number string `json:"mainMebrNo"`
		} `json:"personal"`
	}
	if err := f.request(ctx, "GET", api+"/amcmember-decrypt/api/v1/cookie?ctryCod=eu", nil, &member); err != nil {
		return err
	}
	if member.Personal.Number != f.config.MemberNumber {
		return award.Failure("login_unverified", "ANA did not return the requested authenticated member")
	}
	return nil
}
