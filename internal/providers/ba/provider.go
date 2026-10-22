// Package ba implements British Airways Avios searches.
package ba

import (
	"context"
	"errors"
	"io"
	"time"

	http "github.com/bogdanfinn/fhttp"

	"awarddesk/internal/antibot"
	"awarddesk/internal/award"
	"awarddesk/internal/session"
)

// Config is immutable provider configuration. Credentials never enter results.
type Config struct {
	Email, Password, Proxy string
	Captcha                antibot.Captcha
	Challenge              antibot.Challenge
}

// Provider serializes searches for its single configured account. Each search
// still owns a fresh HTTP session; callers cannot overlap credential submissions.
type Provider struct {
	config Config
	gate   chan struct{}
}

func New(c Config) *Provider { return &Provider{config: c, gate: make(chan struct{}, 1)} }
func (p *Provider) Capabilities() award.Capabilities {
	return award.Capabilities{ID: "ba", Name: "British Airways", Program: "Avios", Cabins: []award.Cabin{award.Economy, award.PremiumEconomy, award.Business, award.First}, VerifiedCabins: []award.Cabin{award.Economy}, MaxAdults: 9, OneWayOnly: true, Currency: "DKK"}
}

// runner owns a single fresh login/booking session and its rotating form state.
type runner struct {
	ctx     context.Context
	client  *session.Client
	config  Config
	report  award.Reporter
	flow    flow
	tokens  tokens
	classic tokens
	// Optional internal diagnostic observer; never configured by the web app.
	observe func(string, string, *http.Response, []byte)
}

func (r *runner) progress(stage, message string) {
	if r.report != nil {
		r.report(award.Progress{Stage: stage, Message: message})
	}
}
func (r *runner) request(m, u string, b io.Reader, h http.Header) (*http.Response, []byte, error) {
	res, body, err := r.client.Do(r.ctx, m, u, b, h)
	if r.observe != nil {
		r.observe(m, u, res, body)
	}
	return res, body, err
}
func (p *Provider) Search(ctx context.Context, q award.Query, report award.Reporter) (award.Result, error) {
	if e := q.Validate(time.Now()); e != nil {
		return award.Result{}, e
	}
	if q.Provider != "ba" {
		return award.Result{}, errors.New("provider mismatch")
	}
	if err := ctx.Err(); err != nil {
		return award.Result{Query: q}, err
	}
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-ctx.Done():
		return award.Result{Query: q}, ctx.Err()
	}
	c := p.config
	if c.Email == "" || c.Password == "" || c.Captcha == nil || c.Challenge == nil {
		return award.Result{}, award.Failure("configuration", "BA account and solver configuration is incomplete")
	}
	client, e := session.New(c.Proxy, []string{"www.britishairways.com", "accounts.britishairways.com", "api.ipify.org"})
	if e != nil {
		return award.Result{}, e
	}
	defer client.Close()
	r := runner{ctx: ctx, client: client, config: c, report: report}
	r.progress("login", "Creating a fresh BA session")
	if e = r.bootstrap(); e != nil {
		return award.Result{}, e
	}
	if e = r.prepare(); e != nil {
		return award.Result{}, e
	}
	if e = r.login(); e != nil {
		return award.Result{}, e
	}
	r.progress("session", "Preparing the award-search session")
	if e = r.finalize(); e != nil {
		return award.Result{}, e
	}
	return r.search(q)
}

// Establish the security session before creating authorization state. All
// cookies stay in this runner's fresh jar; no captured browser state is used.
func (r *runner) bootstrap() error {
	const entry = "https://www.britishairways.com/travel/home/public/en_dk/"
	r.progress("challenge", "Initializing BA security checks before login")
	_, body, err := r.request("GET", entry, nil, nil)
	if err != nil {
		return err
	}
	return r.config.Challenge.Prepare(r.ctx, r.client, entry, body)
}
