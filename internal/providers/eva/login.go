// Package eva implements EVA member login for the Miles & Cash booking flow.
package eva

import (
	"awarddesk/internal/antibot"
	"awarddesk/internal/award"
	"awarddesk/internal/outlook"
	"awarddesk/internal/proxies"
	"awarddesk/internal/session"
	"awarddesk/internal/sessionstore"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	http "github.com/bogdanfinn/fhttp"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const LoginURL = "https://eservice.evaair.com/flyeva/eva/ffp/login.aspx"
const ItineraryURL = "https://eservice.evaair.com/flyeva/eva/ffp/frequent-flyer.aspx"

type Config struct {
	Email, Password, HyperKey, AnyKey string
	ImpervaSolver                     string
	Pool                              *proxies.Pool
	Mailbox                           *outlook.Reader
}
type Provider struct {
	config Config
	gate   chan struct{}
}

func New(c Config) *Provider { return &Provider{config: c, gate: make(chan struct{}, 1)} }

type cached struct {
	Version         int
	Identity        string
	Created         time.Time
	Proxy, Template string
	HTTP            session.State
}

func (p *Provider) identity() string {
	h := sha256.Sum256([]byte(p.config.Email + "\x00" + p.config.Password + "\x00" + session.UserAgent))
	return hex.EncodeToString(h[:])
}
func (p *Provider) store() sessionstore.Store {
	h := sha256.Sum256([]byte(strings.ToLower(p.config.Email)))
	return sessionstore.Store{Path: filepath.Join(".state", "eva", hex.EncodeToString(h[:])+".session")}
}
func (p *Provider) Ensure(ctx context.Context, report award.Reporter) error {
	return p.withSession(ctx, report, nil)
}
func (p *Provider) withSession(ctx context.Context, report award.Reporter, use func(*session.Client, string) error) error {
	if p.config.Email == "" || p.config.Password == "" || p.config.Pool == nil || p.config.Mailbox == nil {
		return award.Failure("configuration", "EVA login, proxy pool and Outlook OAuth must be configured")
	}
	timeout := 6 * time.Minute
	if use != nil {
		// A search may encounter both a mandatory Akamai wait and Imperva's
		// additional CAPTCHA before pricing. Login-only checks stay bounded.
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	store := p.store()
	unlock, err := store.Lock()
	if err != nil {
		return award.Failure("busy", "EVA session store is locked")
	}
	defer unlock()
	return sessionAttempts(func() error { return p.ensureLocked(ctx, report, store, use) }, store.Delete, report)
}

func sessionAttempts(run func() error, reset func() error, report award.Reporter) error {
	renewedSession, retriedCaptcha := false, false
	for attempt := 0; attempt < 3; attempt++ {
		err := run()
		var rejected *award.Error
		if errors.As(err, &rejected) && rejected.Code == "captcha_incorrect" && !retriedCaptcha {
			retriedCaptcha = true
			if report != nil {
				report(award.Progress{Stage: "captcha retry", Message: "EVA rejected the image CAPTCHA; starting one fresh verification attempt"})
			}
			continue
		}
		var renew *searchRenewal
		if errors.As(err, &renew) {
			if renewedSession {
				return renew.cause
			}
			renewedSession = true
			if err = reset(); err != nil {
				return award.Failure("session_store", "Could not replace the interrupted EVA search session")
			}
			if report != nil {
				report(award.Progress{Stage: "session renewal", Message: "The proxy connection interrupted search preparation; reopening on a fresh sticky session"})
			}
			continue
		}
		if !errors.Is(err, errExpired) {
			return err
		}
		if renewedSession {
			return award.Failure("session_expired", "EVA session could not be renewed")
		}
		renewedSession = true
	}
	return award.Failure("session_expired", "EVA session could not be renewed")
}

var errExpired = errors.New("EVA session expired")

func (p *Provider) ensureLocked(ctx context.Context, report award.Reporter, store sessionstore.Store, use func(*session.Client, string) error) (resultErr error) {
	var saved cached
	var lease *proxies.Lease
	data, err := store.Load()
	if err == nil {
		if json.Unmarshal(data, &saved) != nil || saved.Version != 1 {
			return award.Failure("session_store", "EVA saved session is invalid")
		}
		if saved.Identity == p.identity() && !saved.Created.IsZero() && !saved.Created.After(time.Now()) && time.Since(saved.Created) < 24*time.Hour {
			lease, err = p.config.Pool.Resume(ctx, saved.Template, saved.Proxy)
			if err != nil {
				return award.Failure("proxy_pool", "EVA saved proxy is unavailable")
			}
		} else if err = store.Delete(); err != nil {
			return award.Failure("session_store", "Could not remove expired EVA session")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return award.Failure("session_store", "Could not read EVA saved session")
	}
	if lease == nil {
		lease, err = p.config.Pool.Acquire(ctx)
		if err != nil {
			return award.Failure("proxy_pool", "No unused EVA proxy is available")
		}
		saved = cached{Version: 1, Identity: p.identity(), Created: time.Now(), Proxy: lease.URL, Template: lease.Template}
	}
	failedProxy := false
	defer func() { lease.Release(failedProxy) }()
	c, err := session.NewWithTimeout(lease.URL, []string{"booking.evaair.com", "eservice.evaair.com", "book.evaair.com", "api.ipify.org"}, 90*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	progress := func(stage, message string) {
		if report != nil {
			report(award.Progress{Stage: stage, Message: message})
		}
	}
	if saved.HTTP.UserAgent != "" {
		progress("proxy verification", "Checking the saved EVA proxy connection")
		if err = c.Restore(saved.HTTP); err != nil {
			return award.Failure("session_store", "Could not restore EVA session")
		}
		if err = (antibot.Hyper{Key: p.config.HyperKey}).VerifyConnection(ctx, c); err != nil {
			if proxyNeedsRenewal(err) {
				failedProxy = true
				if e := store.Delete(); e != nil {
					return award.Failure("session_store", "Could not replace the unusable EVA session")
				}
				progress("session renewal", "The saved proxy is unavailable; starting a fresh session on another sticky proxy")
				return errExpired
			}
			return err
		}
		progress("session verification", "Validating the saved EVA member session")
		body, target, e := navigate(ctx, c, "GET", ItineraryURL, nil)
		if e != nil {
			return e
		}
		if authenticated(body, target) {
			return p.finish(store, c, saved, use)
		}
		// A redirect to the member login is the only supported expiry signal.
		f, e := parseForm(body, target)
		if e != nil || f.field("$txt_Member") == "" {
			return award.Failure("session_unverified", "EVA saved session status could not be verified")
		}
		if err = store.Delete(); err != nil {
			return award.Failure("session_store", "Could not remove expired EVA session")
		}
		return errExpired
	}
	credentialsSubmitted := false
	defer func() {
		if !credentialsSubmitted && proxyNeedsRenewal(resultErr) {
			failedProxy = true
			resultErr = &searchRenewal{cause: resultErr}
		}
	}()
	progress("proxy verification", "Checking the fresh EVA proxy before login")
	if err = (antibot.Hyper{Key: p.config.HyperKey}).VerifyConnection(ctx, c); err != nil {
		failedProxy = true
		return err
	}
	progress("login page", "Opening EVA's member login")
	body, target, err := navigate(ctx, c, "GET", LoginURL, nil)
	if err != nil {
		return err
	}
	f, err := parseForm(body, target)
	if err != nil {
		return award.Failure("login_protocol", "EVA login page format changed")
	}
	if f.field("$txt_Member") == "" {
		return award.Failure("login_protocol", "EVA member login fields are missing")
	}
	if err = prepareLoginForm(&f, target, time.Now()); err != nil {
		return err
	}
	progress("captcha", "Solving EVA reCAPTCHA v3")
	if err = p.solve(ctx, c, lease.URL, target, &f); err != nil {
		return err
	}
	if f.captchaImage != "" {
		progress("image captcha", "Reading EVA's fallback image CAPTCHA")
		u, e := url.Parse(f.captchaImage)
		if e != nil || u.Scheme != "https" || u.Host != "eservice.evaair.com" {
			return award.Failure("captcha_changed", "EVA CAPTCHA image origin changed")
		}
		response, image, e := c.Do(ctx, "GET", f.captchaImage, nil, http.Header{"sec-fetch-site": {"same-origin"}, "referer": {target}, "sec-fetch-dest": {"image"}, "sec-fetch-mode": {"no-cors"}})
		if e != nil {
			return e
		}
		if !strings.HasPrefix(response.Header.Get("Content-Type"), "image/") {
			return award.Failure("captcha_changed", "EVA CAPTCHA image unavailable")
		}
		code, e := (antibot.AnySolver{Key: p.config.AnyKey}).SolveText(ctx, target, image)
		if e != nil {
			return e
		}
		if e = f.set("$txt_CaptchaCode", code); e != nil {
			return e
		}
	}
	if p.config.HyperKey != "" {
		progress("security", "Initializing EVA browser security before credential submission")
		if err = (antibot.Hyper{Key: p.config.HyperKey, SkipPixel: true}).Prepare(ctx, c, target, body); err != nil {
			return err
		}
	}
	progress("mailbox", "Checking Outlook before requesting an OTP")
	point, err := p.config.Mailbox.Checkpoint(ctx)
	if err != nil {
		return award.Failure("mailbox", err.Error())
	}
	if err = f.set("$txt_Member", p.config.Email); err != nil {
		return err
	}
	if err = f.set("$txt_Password", p.config.Password); err != nil {
		return err
	}
	if err = f.set("__MasterEVENTTARGET", "ctl00$content$wuc_login$btn_Login_Server"); err != nil {
		return err
	}
	f.set("__MasterEVENTARGUMENT", "")
	progress("credentials", "Submitting EVA credentials once")
	credentialsSubmitted = true
	body, target, err = navigate(ctx, c, "POST", f.action, f.values)
	if err != nil {
		return err
	}
	body, target, err = p.resumeChallenge(ctx, c, body, target, progress)
	if err != nil {
		return err
	}
	next, parseErr := parseForm(body, target)
	if parseErr == nil && next.field("$txt_CertificationCode") != "" {
		if err = prepareLoginForm(&next, target, time.Now()); err != nil {
			return err
		}
		progress("otp", "Waiting for a fresh EVA OTP in Outlook")
		// The returned page contains fresh ASP.NET state. Never reuse the login
		// request's ZIPSTATE, CSRF token, or a code from a previous operation.
		if next.key != "" {
			if err = p.solve(ctx, c, lease.URL, target, &next); err != nil {
				return err
			}
		}
		code, e := p.config.Mailbox.Wait(ctx, point)
		if e != nil {
			return award.Failure("otp", e.Error())
		}
		if err = next.set("$txt_CertificationCode", code); err != nil {
			return err
		}
		if err = next.set("__MasterEVENTTARGET", "ctl00$content$wuc_login$wuc_SSE$btn_TokenChecked"); err != nil {
			return err
		}
		if err = next.set("__MasterEVENTARGUMENT", "undefined"); err != nil {
			return err
		}
		progress("otp submission", "Submitting the current EVA verification code once")
		body, target, err = navigate(ctx, c, "POST", next.action, next.values)
		if err != nil {
			return err
		}
		body, target, err = p.resumeChallenge(ctx, c, body, target, progress)
		if err != nil {
			return err
		}
	}
	if !authenticated(body, target) {
		if feedback := loginFeedback(body, p.config.Email, p.config.Password); feedback != "" {
			if parseErr == nil && imageCaptchaRejected(f, next, feedback) {
				return award.Failure("captcha_incorrect", "EVA explicitly rejected the image CAPTCHA")
			}
			return award.Failure("login_response", "EVA login response: "+feedback)
		}
		return award.Failure("login_unverified", "EVA did not open the authenticated member page; no session was saved")
	}
	err = p.finish(store, c, saved, use)
	if err == nil {
		progress("session ready", "EVA member session verified and encrypted for reuse")
	}
	return err
}

func imageCaptchaRejected(original, next form, feedback string) bool {
	return original.captchaImage != "" && next.field("$txt_Member") != "" && strings.EqualFold(strings.TrimSpace(feedback), "Wrong CAPTCHA entry. Please try again.")
}

// Classify transport failures only. Callers must separately establish that
// the current stage permits recovery without replaying credentials or pricing.
func proxyNeedsRenewal(err error) bool {
	var e *award.Error
	return errors.As(err, &e) && (e.Code == "connection" || e.Code == "proxy_changed")
}
func (p *Provider) resumeChallenge(ctx context.Context, c *session.Client, body []byte, target string, progress func(string, string)) ([]byte, string, error) {
	if !strings.Contains(string(body), `name="sec_chlge_forward_wrap"`) {
		return body, target, nil
	}
	f, err := parseForm(body, target)
	if err != nil {
		return nil, target, err
	}
	u, err := url.Parse(f.action)
	if err != nil || u.Scheme != "https" || u.Host != "eservice.evaair.com" || !strings.EqualFold(u.Path, "/flyeva/eva/ffp/login.aspx") || u.Query().Get("akamai-challenge-resubmit") != "true" {
		return nil, target, award.Failure("challenge_changed", "EVA challenge forwarding destination changed")
	}
	progress("adaptive security", "Completing EVA's additional security check, including its required wait")
	if err = (antibot.Hyper{Key: p.config.HyperKey}).PrepareAdaptive(ctx, c, target, body); err != nil {
		return nil, target, err
	}
	// Forward only EVA's fresh opaque wrapper after explicit verification;
	// never retry the original credential request on a timeout or rejection.
	values := url.Values{}
	for _, key := range []string{"sec_chlge_forward_wrap", "sec_chlge_content_type_wrap"} {
		if f.values.Get(key) == "" {
			return nil, target, award.Failure("challenge_changed", "EVA challenge forwarding data missing")
		}
		values.Set(key, f.values.Get(key))
	}
	return navigate(ctx, c, "POST", f.action, values)
}

// EVA's getClientDT JavaScript fills these initially empty fields on page load.
// The timestamp is Taiwan local time, independent of the runner's timezone.
func prepareLoginForm(f *form, target string, now time.Time) error {
	if err := f.set("$hid_url", target); err != nil {
		return award.Failure("login_protocol", "EVA login URL field changed")
	}
	if err := f.set("$hid_DateTime", now.In(time.FixedZone("Taipei", 8*60*60)).Format("2006/01/02 15:04:05")); err != nil {
		return award.Failure("login_protocol", "EVA login timestamp field changed")
	}
	return nil
}
func (p *Provider) finish(store sessionstore.Store, c *session.Client, saved cached, use func(*session.Client, string) error) error {
	if err := p.save(store, c, saved); err != nil {
		return err
	}
	if use == nil {
		return nil
	}
	err := use(c, saved.Proxy)
	saveErr := p.save(store, c, saved)
	if err != nil {
		return err
	}
	return saveErr
}
func (p *Provider) solve(ctx context.Context, c *session.Client, proxy, page string, f *form) error {
	if f.key == "" || f.captchaAction == "" {
		return award.Failure("captcha_changed", "EVA login does not expose the expected reCAPTCHA v3 configuration")
	}
	solution, err := (antibot.AnySolver{Key: p.config.AnyKey, Proxy: proxy, UserAgent: c.UserAgent()}).SolveRecaptchaV3(ctx, page, f.key, f.captchaAction, f.title)
	if err != nil {
		return award.Failure("captcha", err.Error())
	}
	return f.set("$grecaptcharesponse", solution.Token)
}
func (p *Provider) save(store sessionstore.Store, c *session.Client, saved cached) error {
	state, err := c.Snapshot()
	if err != nil {
		return award.Failure("session_store", "Could not snapshot EVA session")
	}
	saved.HTTP = state
	data, err := json.Marshal(saved)
	if err == nil {
		err = store.Save(data)
	}
	if err != nil {
		return award.Failure("session_store", "Could not save EVA session")
	}
	return nil
}
func authenticated(body []byte, target string) bool {
	u, err := url.Parse(target)
	if err != nil || u.Host != "eservice.evaair.com" || strings.ToLower(u.Path) != "/flyeva/eva/ffp/frequent-flyer.aspx" {
		return false
	}
	f, err := parseForm(body, target)
	return err == nil && f.field("$txt_Member") == "" && strings.Contains(string(body), `id="Member_Name"`) && strings.Contains(string(body), `__doPostBackLocal('FUN', 'logout')`)
}
func navigate(ctx context.Context, c *session.Client, method, target string, values url.Values, from ...string) ([]byte, string, error) {
	previous := LoginURL
	if len(from) > 0 {
		previous = from[0]
	} else if method == "GET" && target == LoginURL {
		previous = ""
	}
	for i := 0; i < 6; i++ {
		u, err := url.Parse(target)
		if err != nil || u.Scheme != "https" || u.User != nil || (u.Host != "booking.evaair.com" && u.Host != "eservice.evaair.com" && u.Host != "book.evaair.com") {
			return nil, "", award.Failure("unsafe_destination", "EVA navigation left the allowed portal")
		}
		var body *strings.Reader
		h := http.Header{"sec-fetch-site": {"none"}}
		var source *url.URL
		if previous != "" {
			source, err = url.Parse(previous)
			if err != nil || source.Scheme != "https" || !strings.HasSuffix(source.Hostname(), ".evaair.com") {
				return nil, target, award.Failure("unsafe_destination", "Invalid EVA referring page")
			}
			h["referer"] = []string{previous}
			h["sec-fetch-site"] = []string{"same-site"}
			if source.Host == u.Host {
				h["sec-fetch-site"] = []string{"same-origin"}
			}
		}
		if method == "POST" {
			body = strings.NewReader(values.Encode())
			h["content-type"] = []string{"application/x-www-form-urlencoded"}
			if source == nil {
				return nil, target, award.Failure("unsafe_destination", "EVA form origin missing")
			}
			h["origin"] = []string{source.Scheme + "://" + source.Host}
		}
		var res *http.Response
		var data []byte
		if body != nil {
			res, data, err = c.Do(ctx, method, target, body, h)
		} else {
			res, data, err = c.Do(ctx, method, target, nil, h)
		}
		if err != nil {
			if res != nil && (res.StatusCode == 403 || res.StatusCode == 428) && isAdaptiveBookingChallenge(data, target) {
				return data, target, nil
			}
			if res != nil && res.StatusCode == 403 && u.Host == "book.evaair.com" && isReeseContinuation(data, target) {
				return data, target, nil
			}
			if res != nil && res.StatusCode == 403 {
				kind := "security"
				if bytes.Contains(data, []byte("sec_chlge_forward_wrap")) {
					kind = "additional Akamai challenge"
				} else if bytes.Contains(data, []byte("Access Denied")) {
					kind = "access policy"
				}
				return nil, target, &award.Error{Code: "session_denied", HTTPStatus: 403, Message: "EVA " + u.Host + " rejected the request (HTTP 403; " + kind + ")"}
			}
			return nil, target, err
		}
		if res.StatusCode == 200 {
			return data, target, nil
		}
		if res.StatusCode != 301 && res.StatusCode != 302 && res.StatusCode != 303 {
			return nil, target, award.Failure("login_http", "EVA returned an unexpected navigation response")
		}
		ref, err := url.Parse(res.Header.Get("Location"))
		if err != nil || ref.String() == "" {
			return nil, target, award.Failure("login_protocol", "EVA redirect missing")
		}
		previous = target
		target = u.ResolveReference(ref).String()
		method = "GET"
		values = nil
	}
	return nil, target, award.Failure("redirect_limit", "EVA redirected too many times")
}
