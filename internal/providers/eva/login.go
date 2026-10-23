package eva

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"

	"awarddesk/internal/antibot"
	"awarddesk/internal/award"
	"awarddesk/internal/proxies"
	"awarddesk/internal/session"
	"awarddesk/internal/sessionstore"
)

func (p *Provider) Ensure(ctx context.Context, report award.Reporter) error {
	ctx, report = trackRequestRecoveries(ctx, report)
	stage := "session preparation"
	observer := report
	err := p.withSession(ctx, func(progress award.Progress) {
		stage = progress.Stage
		if observer != nil {
			observer(progress)
		}
	}, nil)
	return loginStageError(err, stage)
}
func loginStageError(err error, stage string) error {
	var failure *award.Error
	if !errors.As(err, &failure) {
		return err
	}
	copy := *failure
	if copy.Stage == "" {
		copy.Stage = stage
	}
	if (stage == "credentials" || stage == "otp submission" || stage == "verification continuation") && (copy.Code == "connection" || copy.Code == "response_read") {
		copy.OutcomeUnknown = true
	}
	return &copy
}

func (p *Provider) withSession(ctx context.Context, report award.Reporter, use func(*session.Client, string) error) error {
	if p.config.Email == "" || p.config.Password == "" || p.config.Pool == nil || p.config.Mailbox == nil {
		return award.Failure("configuration", "EVA login, proxy pool and Outlook OAuth must be configured")
	}
	timeout := 6 * time.Minute
	if use != nil {

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
	if p.closed {
		return award.Failure("closed", "EVA session service is closed")
	}
	store := p.store()
	unlock, err := store.Lock()
	if err != nil {
		return award.Failure("busy", "EVA session store is locked")
	}
	defer unlock()
	if savedSessionOnly(ctx) {

		err = p.ensureLocked(ctx, report, store, use)
	} else {
		err = sessionAttempts(func() error { return p.ensureLocked(ctx, report, store, use) }, func() error { p.discardWarm(); return store.Delete() }, report)
	}
	if err != nil {
		p.discardWarm()
	}
	return err
}

func sessionAttempts(run func() error, reset func() error, report award.Reporter) error {
	renewedSession, retriedCaptcha := false, false
	for attempt := 0; attempt < 3; attempt++ {
		err := run()
		var rejected *award.Error
		if errors.As(err, &rejected) && rejected.Code == "captcha_incorrect" && !retriedCaptcha {
			retriedCaptcha = true
			if report != nil {
				report(award.Progress{Stage: "captcha retry", Message: "EVA rejected the image CAPTCHA; starting one fresh verification attempt", Recovery: recoveryDetail("credentials", err, attempt+1)})
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
				report(award.Progress{Stage: "session renewal", Message: "EVA session setup was interrupted; retrying with a fresh sticky session", Recovery: recoveryDetail("session preparation", renew.cause, attempt+1)})
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
		if saved.Identity == p.identity() && !saved.Created.IsZero() && !saved.Created.After(time.Now()) && time.Now().Before(p.sessionExpiry(saved)) {
			lease, err = p.config.Pool.Resume(ctx, saved.Template, saved.Proxy)
			if errors.Is(err, proxies.ErrUnavailable) {
				if report != nil {
					report(award.Progress{Stage: "proxy queue", Message: "Waiting for the saved session's sticky proxy to become available"})
				}

				lease, err = p.config.Pool.WaitResume(ctx, saved.Template, saved.Proxy)
			}
			if err != nil {
				if errors.Is(err, proxies.ErrRemoved) {
					p.discardWarm()
					if e := store.Delete(); e != nil {
						return award.Failure("session_store", "Could not replace the removed EVA proxy session")
					}
					return errExpired
				}
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
	defer func() {
		var denied *award.Error
		if errors.As(resultErr, &denied) && denied.Code == "session_denied" && denied.HTTPStatus == 403 {
			failedProxy = true
		}
		lease.Release(failedProxy)
	}()
	c, reused, err := p.sessionClient(lease.URL, data, saved.HTTP.UserAgent != "")
	if err != nil {
		return err
	}
	defer func() {
		if resultErr != nil {
			p.discardWarm()
		}
	}()
	progress := func(stage, message string) {
		if report != nil {
			report(award.Progress{Stage: stage, Message: message})
		}
	}
	if saved.HTTP.UserAgent != "" {
		progress("proxy verification", "Checking the saved EVA proxy connection")
		if !reused {
			if err = c.Restore(saved.HTTP); err != nil {
				return award.Failure("session_store", "Could not restore EVA session")
			}
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
		if use != nil && reused && p.recentlyVerified(time.Now()) {
			progress("session reuse", "Reusing the recently verified EVA member session")
			return p.finish(store, c, saved, use)
		}
		progress("session verification", "Validating the saved EVA member session")
		body, target, e := verifySavedSession(ctx, func() ([]byte, string, error) { return navigate(ctx, c, "GET", ItineraryURL, nil) }, report)
		if e != nil {
			var renewal *searchRenewal
			if errors.As(e, &renewal) {
				failedProxy = true
			}
			return e
		}
		if authenticated(body, target) {
			p.markVerified(time.Now())
			return p.finish(store, c, saved, use)
		}

		f, e := parseForm(body, target)
		if e != nil || f.field("$txt_Member") == "" {
			return award.Failure("session_unverified", "EVA saved session status could not be verified")
		}
		if err = store.Delete(); err != nil {
			return award.Failure("session_store", "Could not remove expired EVA session")
		}
		return errExpired
	}
	if savedSessionOnly(ctx) {
		return award.Failure("session_expired", "EVA tax lookup needs an existing member session")
	}
	credentialsSubmitted := false
	defer func() {
		if !credentialsSubmitted && canRecoverSessionSetup(resultErr) && ctx.Err() == nil {
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
		if err = (antibot.Hyper{Key: p.config.HyperKey}).Prepare(ctx, c, target, body); err != nil {
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
	p.markVerified(time.Now())
	err = p.finish(store, c, saved, use)
	if err == nil {
		progress("session ready", "EVA member session verified and saved for reuse")
	}
	return err
}

func imageCaptchaRejected(original, next form, feedback string) bool {
	return original.captchaImage != "" && next.field("$txt_Member") != "" && strings.EqualFold(strings.TrimSpace(feedback), "Wrong CAPTCHA entry. Please try again.")
}

func proxyNeedsRenewal(err error) bool {
	var e *award.Error
	return errors.As(err, &e) && (e.Code == "connection" || e.Code == "proxy_changed" || e.Code == "response_read")
}
func (p *Provider) resumeChallenge(ctx context.Context, c *session.Client, body []byte, target string, progress func(string, string)) ([]byte, string, error) {
	return p.resumePortalChallenge(ctx, c, body, target, "/flyeva/eva/ffp/login.aspx", progress)
}
func (p *Provider) resumePortalChallenge(ctx context.Context, c *session.Client, body []byte, target, expectedPath string, progress func(string, string), beforeForward ...func() error) ([]byte, string, error) {
	if !strings.Contains(string(body), `name="sec_chlge_forward_wrap"`) {
		return body, target, nil
	}
	f, err := parseForm(body, target)
	if err != nil {
		return nil, target, err
	}
	u, err := url.Parse(f.action)
	if err != nil || u.Scheme != "https" || u.Host != "eservice.evaair.com" || !strings.EqualFold(u.Path, expectedPath) || u.Query().Get("akamai-challenge-resubmit") != "true" {
		return nil, target, award.Failure("challenge_changed", "EVA challenge forwarding destination changed")
	}

	values := url.Values{}
	for _, key := range []string{"sec_chlge_forward_wrap", "sec_chlge_content_type_wrap"} {
		if f.values.Get(key) == "" {
			return nil, target, award.Failure("challenge_changed", "EVA challenge forwarding data missing")
		}
		values.Set(key, f.values.Get(key))
	}
	progress("adaptive security", "Completing EVA's additional security check, including its required wait")
	if err = (antibot.Hyper{Key: p.config.HyperKey}).PrepareAdaptive(ctx, c, target, body); err != nil {
		return nil, target, err
	}
	for _, before := range beforeForward {
		if err = before(); err != nil {
			return nil, target, err
		}
	}
	progress("verification continuation", "Continuing EVA's explicitly intercepted request once")
	return navigate(ctx, c, "POST", f.action, values, target)
}

func prepareLoginForm(f *form, target string, now time.Time) error {
	if err := f.set("$hid_url", target); err != nil {
		return award.Failure("login_protocol", "EVA login URL field changed")
	}
	if err := f.set("$hid_DateTime", now.In(time.FixedZone("Taipei", 8*60*60)).Format("2006/01/02 15:04:05")); err != nil {
		return award.Failure("login_protocol", "EVA login timestamp field changed")
	}
	return nil
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
func authenticated(body []byte, target string) bool {
	u, err := url.Parse(target)
	if err != nil || u.Host != "eservice.evaair.com" || strings.ToLower(u.Path) != "/flyeva/eva/ffp/frequent-flyer.aspx" {
		return false
	}
	f, err := parseForm(body, target)
	return err == nil && f.field("$txt_Member") == "" && strings.Contains(string(body), `id="Member_Name"`) && strings.Contains(string(body), `__doPostBackLocal('FUN', 'logout')`)
}
