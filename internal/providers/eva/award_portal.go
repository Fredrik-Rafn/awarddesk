package eva

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"

	"awarddesk/internal/antibot"
	"awarddesk/internal/award"
	"awarddesk/internal/session"
)

func (p *Provider) awardNavigate(ctx context.Context, c *session.Client, proxy, method, target string, values url.Values, from string, report award.Reporter) ([]byte, string, error) {
	started := time.Now()
	defer func() {
		u, _ := url.Parse(target)
		slog.Info("EVA award request", "method", method, "page", u.Path, "elapsed", time.Since(started).Round(time.Millisecond))
	}()
	body, next, err := navigate(ctx, c, method, target, values, from)
	if err != nil {
		return nil, next, err
	}
	body, next, err = p.resumeAwardForward(ctx, c, body, next, report)
	if err != nil {
		return nil, next, err
	}
	f, parseErr := parseForm(body, next)
	if parseErr == nil && f.field("$txt_Member") != "" {
		if _, worker := ctx.Value(awardQuoteTargetsKey{}).([]AwardQuoteTarget); worker {
			return nil, next, award.Failure("award_helper_not_ready", "The helper account has no ready EVA award session")
		}

		if method != "GET" {
			return nil, next, award.Failure("session_expired", "EVA award session expired during the search")
		}
		body, next, err = p.loginAwardPortal(ctx, c, proxy, body, next, report)
		if err != nil {
			return nil, next, err
		}
		phase := "award flights"
		if strings.EqualFold(target, awardAvailabilityURL) {
			phase = "award availability"
		}
		if allowedAwardPage(target, "select-first-journey.aspx") {
			phase = "award pricing"
		}
		awardProgress(report, phase, "EVA award login is ready; continuing the search", 0, 0, nil)

		body, next, err = navigate(ctx, c, "GET", target, nil, next)
		if err != nil {
			return nil, next, err
		}
	}
	if method == "GET" && strings.EqualFold(target, awardAvailabilityURL) {
		awardProgress(report, "award security", "Preparing EVA award availability security", 0, 0, nil)
		if err = (antibot.Hyper{Key: p.config.HyperKey, MinimumSensorRounds: 3}).Prepare(ctx, c, next, body); err != nil {
			return nil, next, err
		}
	}
	return body, next, nil
}

func awardForwardResponse(status int, target string, body []byte) bool {
	if status != 403 && status != 428 {
		return false
	}
	allowed := strings.EqualFold(target, awardAvailabilityURL) || awardLoginPage(target)
	for _, page := range []string{"select-itinerary.aspx", "select-first-journey.aspx", "mileage-deducted.aspx"} {
		allowed = allowed || allowedAwardPage(target, page)
	}
	if !allowed {
		return false
	}
	f, err := parseForm(body, target)
	if err != nil || f.values.Get("sec_chlge_forward_wrap") == "" || f.values.Get("sec_chlge_content_type_wrap") == "" {
		return false
	}
	u, e := url.Parse(f.action)
	source, se := url.Parse(target)
	return e == nil && se == nil && u.Scheme == "https" && u.Host == "booking.evaair.com" && u.User == nil && strings.EqualFold(u.Path, source.Path) && u.Query().Get("akamai-challenge-resubmit") == "true"
}

func awardLoginPage(target string) bool {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.Host != "booking.evaair.com" || u.User != nil {
		return false
	}
	return strings.EqualFold(u.Path, awardTicketPath+"login.aspx") || strings.EqualFold(u.Path, "/flyeva/eva/b2c/plan-your-journey/award-upgrade-availability/login.aspx")
}

func (p *Provider) loginAwardPortal(ctx context.Context, c *session.Client, proxy string, body []byte, target string, report award.Reporter) ([]byte, string, error) {
	if _, worker := ctx.Value(awardQuoteTargetsKey{}).([]AwardQuoteTarget); worker {
		return nil, target, award.Failure("award_helper_not_ready", "The helper account has no ready EVA award session")
	}
	f, err := parseForm(body, target)
	if err != nil || !awardLoginPage(f.action) {
		return nil, target, award.Failure("award_protocol", "EVA award login destination changed")
	}
	if err = prepareLoginForm(&f, target, time.Now()); err != nil {
		return nil, target, err
	}
	awardProgress(report, "captcha", "Preparing EVA award login verification", 0, 0, nil)
	if f.key != "" {
		if err = p.solve(ctx, c, proxy, target, &f); err != nil {
			return nil, target, err
		}
	}
	if f.captchaImage != "" {
		u, e := url.Parse(f.captchaImage)
		if e != nil || u.Scheme != "https" || u.Host != "booking.evaair.com" || u.User != nil {
			return nil, target, award.Failure("captcha_changed", "EVA award image CAPTCHA destination changed")
		}
		response, image, e := c.Do(ctx, "GET", f.captchaImage, nil, http.Header{"referer": {target}, "sec-fetch-site": {"same-origin"}, "sec-fetch-mode": {"no-cors"}, "sec-fetch-dest": {"image"}})
		if e != nil {
			return nil, target, e
		}
		if !strings.HasPrefix(response.Header.Get("Content-Type"), "image/") {
			return nil, target, award.Failure("captcha_changed", "EVA award image CAPTCHA is unavailable")
		}
		code, e := (antibot.AnySolver{Key: p.config.AnyKey}).SolveText(ctx, target, image)
		if e != nil {
			return nil, target, e
		}
		if err = f.set("$txt_CaptchaCode", code); err != nil {
			return nil, target, err
		}
	}
	if f.key == "" && f.captchaImage == "" {
		return nil, target, award.Failure("captcha_changed", "EVA award login verification changed")
	}
	if err = (antibot.Hyper{Key: p.config.HyperKey, MinimumSensorRounds: 3}).Prepare(ctx, c, target, body); err != nil {
		return nil, target, err
	}
	awardProgress(report, "mailbox", "Checking Outlook before requesting award-login verification", 0, 0, nil)
	point, err := p.config.Mailbox.Checkpoint(ctx)
	if err != nil {
		return nil, target, award.Failure("mailbox", err.Error())
	}
	member := f.field("$txt_Member")
	if member == "" || f.field("$txt_Password") == "" {
		return nil, target, award.Failure("award_protocol", "EVA award login fields changed")
	}
	for suffix, value := range map[string]string{"$txt_Member": p.config.Email, "$txt_Password": p.config.Password, "__MasterEVENTTARGET": strings.TrimSuffix(member, "$txt_Member") + "$btn_Login_Server", "__MasterEVENTARGUMENT": ""} {
		if err = f.set(suffix, value); err != nil {
			return nil, target, err
		}
	}
	awardProgress(report, "credentials", "Submitting EVA award credentials once", 0, 0, nil)
	body, target, err = navigate(ctx, c, "POST", f.action, f.values, target)
	if err != nil {
		return nil, target, err
	}
	body, target, err = p.resumeAwardForward(ctx, c, body, target, report)
	if err != nil {
		return nil, target, err
	}
	next, e := parseForm(body, target)
	if e == nil && next.field("$txt_CertificationCode") != "" {
		if !awardLoginPage(next.action) {
			return nil, target, award.Failure("unsafe_destination", "EVA award OTP destination changed")
		}
		if err = prepareLoginForm(&next, target, time.Now()); err != nil {
			return nil, target, err
		}
		if next.key != "" {
			if err = p.solve(ctx, c, proxy, target, &next); err != nil {
				return nil, target, err
			}
		}
		awardProgress(report, "otp", "Waiting patiently for a fresh EVA award-login email", 0, 0, nil)
		code, e := p.config.Mailbox.Wait(ctx, point)
		if e != nil {
			return nil, target, award.Failure("otp", e.Error())
		}
		field := next.field("$txt_CertificationCode")
		for suffix, value := range map[string]string{"$txt_CertificationCode": code, "__MasterEVENTTARGET": strings.TrimSuffix(field, "$txt_CertificationCode") + "$btn_TokenChecked", "__MasterEVENTARGUMENT": "undefined"} {
			if err = next.set(suffix, value); err != nil {
				return nil, target, err
			}
		}
		awardProgress(report, "otp submission", "Submitting the current EVA award verification code once", 0, 0, nil)
		body, target, err = navigate(ctx, c, "POST", next.action, next.values, target)
		if err != nil {
			return nil, target, err
		}
		body, target, err = p.resumeAwardForward(ctx, c, body, target, report)
		if err != nil {
			return nil, target, err
		}
	}
	f, e = parseForm(body, target)
	if e != nil || awardLoginPage(target) || f.field("$txt_Member") != "" {
		return nil, target, award.Failure("login_unverified", "EVA award login did not reach an authenticated page")
	}
	return body, target, nil
}

func (p *Provider) resumeAwardForward(ctx context.Context, c *session.Client, body []byte, target string, report award.Reporter) ([]byte, string, error) {
	if !strings.Contains(string(body), `name="sec_chlge_forward_wrap"`) {
		return body, target, nil
	}
	f, err := parseForm(body, target)
	if err != nil {
		return nil, target, err
	}
	u, e := url.Parse(f.action)
	source, se := url.Parse(target)
	if e != nil || se != nil || u.Scheme != "https" || u.Host != "booking.evaair.com" || u.User != nil || !strings.EqualFold(u.Path, source.Path) || u.Query().Get("akamai-challenge-resubmit") != "true" {
		return nil, target, award.Failure("challenge_changed", "EVA award challenge continuation changed")
	}
	v := url.Values{}
	for _, key := range []string{"sec_chlge_forward_wrap", "sec_chlge_content_type_wrap"} {
		if f.values.Get(key) == "" {
			return nil, target, award.Failure("challenge_changed", "EVA award challenge continuation is missing")
		}
		v.Set(key, f.values.Get(key))
	}
	stage := awardContinuationStage(target)
	securityStage := "adaptive security"
	if stage != "verification continuation" {
		securityStage = "award security"
	}
	awardProgress(report, securityStage, "Completing EVA's required award security check", 0, 0, nil)
	if err = (antibot.Hyper{Key: p.config.HyperKey}).PrepareAdaptive(ctx, c, target, body); err != nil {
		return nil, target, err
	}
	awardProgress(report, stage, "Continuing the explicitly intercepted award request once", 0, 0, nil)
	return navigate(ctx, c, "POST", f.action, v, target)
}

func awardContinuationStage(target string) string {
	if strings.EqualFold(target, awardAvailabilityURL) {
		return "award availability"
	}
	if allowedAwardPage(target, "select-itinerary.aspx") {
		return "award flights"
	}
	if allowedAwardPage(target, "select-first-journey.aspx") || allowedAwardPage(target, "mileage-deducted.aspx") {
		return "award pricing"
	}
	return "verification continuation"
}
