package eva

import (
	"bytes"
	"context"
	"net/url"
	"strings"

	http "github.com/bogdanfinn/fhttp"
	"golang.org/x/net/html"

	"awarddesk/internal/antibot"
	"awarddesk/internal/award"
	"awarddesk/internal/session"
)

func cashConfirmationForm(body []byte, target string) (form, bool) {
	if !bytes.Contains(body, []byte("TRANSBLOCK")) {
		return form{}, false
	}
	current, err := url.Parse(target)
	if err != nil || current.Scheme != "https" || current.Host != "booking.evaair.com" || current.User != nil {
		return form{}, false
	}
	f, err := parseForm(body, target)
	if err != nil {
		return form{}, false
	}
	u, err := url.Parse(f.action)
	if err != nil || u.Scheme != "https" || u.Host != "booking.evaair.com" || u.User != nil || !strings.EqualFold(u.Path, "/flyeva/EVA/B2C/booking-confirm.aspx") || f.values.Get("message") != "TRANSBLOCK" {
		return form{}, false
	}
	for _, name := range []string{"message", "normal_message", "data", "transblock_pass"} {
		if len(f.values[name]) != 1 || f.values.Get(name) == "" {
			return form{}, false
		}
	}
	root, err := awardDocument(body)
	if err != nil {
		return form{}, false
	}
	forms := awardNodes(root, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "form" })
	if len(forms) != 1 || awardAttr(forms[0], "id") != "PostForm" || !strings.EqualFold(awardAttr(forms[0], "method"), "POST") {
		return form{}, false
	}
	return f, true
}

func continueCashConfirmation(ctx context.Context, body []byte, target string, post func(context.Context, string, url.Values, string) ([]byte, string, error)) ([]byte, string, error) {
	f, ok := cashConfirmationForm(body, target)
	if !ok {
		return body, target, nil
	}
	if err := ctx.Err(); err != nil {
		return body, target, err
	}
	return post(ctx, f.action, f.values, target)
}

func cashConfirmationPage(body []byte, target string) (form, bool) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.Host != "booking.evaair.com" || u.User != nil || !strings.EqualFold(u.Path, "/flyeva/EVA/B2C/booking-confirm.aspx") {
		return form{}, false
	}
	f, err := parseForm(body, target)
	if err != nil || f.action != target || f.captchaAction != "BookingOnline" || f.key == "" || f.field("$tb_Captcha") == "" || f.field("$hi_RecaptchaResult") == "" || f.field("$hi_RecaptchaData") == "" || f.field("__EVENTTARGET") == "" {
		return form{}, false
	}
	return f, true
}

func prepareCashConfirmation(f *form, verified bookingRecaptchaResult, imageCode string) error {
	if (verified.Success != "true" && verified.Success != "false") || verified.Source == "" {
		return award.Failure("captcha_changed", "EVA booking confirmation verification format changed")
	}
	code := "******"
	if verified.Success != "true" {
		code = imageCode
		if len(code) < 4 || len(code) > 6 {
			return award.Failure("captcha_changed", "EVA booking confirmation image code is invalid")
		}
		for _, r := range code {
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
				return award.Failure("captcha_changed", "EVA booking confirmation image code is invalid")
			}
		}
	}
	prefix := strings.TrimSuffix(f.field("$tb_Captcha"), "tb_Captcha")
	for name, values := range f.values {
		if strings.HasPrefix(name, "BDC_BackWorkaround_") && len(values) == 1 && values[0] == "0" {
			f.values.Set(name, "1")
		}
	}
	for suffix, value := range map[string]string{"$tb_Captcha": code, "$hi_RecaptchaResult": verified.Success, "$hi_RecaptchaData": verified.Source, "__EVENTTARGET": prefix + "btn_Confirm", "__EVENTARGUMENT": ""} {
		if err := f.set(suffix, value); err != nil {
			return award.Failure("search_protocol", "EVA booking confirmation fields changed")
		}
	}
	return nil
}

func cashConfirmationFeedback(body []byte) string {
	root, err := awardDocument(body)
	if err != nil {
		return ""
	}
	nodes := awardNodes(root, func(n *html.Node) bool {
		return n.Type == html.ElementNode && awardAttr(n, "id") == "content_wuc_Error"
	})
	if len(nodes) != 1 {
		return ""
	}
	return awardText(nodes[0])
}

func (p *Provider) completeCashConfirmation(ctx context.Context, c *session.Client, proxy string, body []byte, target string, report award.Reporter) ([]byte, string, error) {
	body, target, err := continueCashConfirmation(ctx, body, target, func(ctx context.Context, next string, values url.Values, from string) ([]byte, string, error) {
		return navigate(ctx, c, "POST", next, values, from)
	})
	if err != nil {
		return body, target, err
	}
	return retryCashConfirmation(ctx, body, target, report, func(ctx context.Context, body []byte, target string, f form, attempt int) ([]byte, string, error) {
		return p.submitCashConfirmation(ctx, c, proxy, body, target, f, attempt)
	})
}

func retryCashConfirmation(ctx context.Context, body []byte, target string, report award.Reporter, submit func(context.Context, []byte, string, form, int) ([]byte, string, error)) ([]byte, string, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return body, target, err
		}
		f, ok := cashConfirmationPage(body, target)
		if !ok {
			return body, target, nil
		}
		if report != nil {
			report(award.Progress{Stage: "booking confirmation", Message: "Completing EVA's route-specific booking verification"})
		}
		var err error
		body, target, err = submit(ctx, body, target, f, attempt)
		if err != nil {
			return body, target, err
		}
		if _, ok = cashConfirmationPage(body, target); !ok {
			return body, target, nil
		}
		if !strings.EqualFold(cashConfirmationFeedback(body), "Incorrect verification code. Please enter again.") {
			return body, target, nil
		}
		failure := award.Failure("captcha_incorrect", "EVA explicitly rejected the booking confirmation verification code")
		if attempt >= 2 {
			return body, target, failure
		}
		if report != nil {
			report(award.Progress{Stage: "booking confirmation", Message: "EVA rejected the verification code; solving the newly returned confirmation form", Recovery: recoveryDetail("booking confirmation", failure, attempt+1)})
		}
	}
}

func (p *Provider) submitCashConfirmation(ctx context.Context, c *session.Client, proxy string, body []byte, target string, f form, attempt int) ([]byte, string, error) {
	solver := antibot.AnySolver{Key: p.config.AnyKey, Proxy: proxy, UserAgent: c.UserAgent(), RecaptchaMinScore: 0.9, RecaptchaProviderOrder: []string{"TwoCaptcha", "CapSolver"}}
	if attempt%2 == 1 {
		solver.RecaptchaProviderOrder = []string{"CapSolver", "TwoCaptcha"}
	}
	solution, err := bookingVerification(ctx, func(ctx context.Context) (antibot.CaptchaSolution, error) {
		return solver.SolveRecaptchaV3(ctx, target, f.key, f.captchaAction, f.title)
	}, func(ctx context.Context) error {
		return (antibot.Hyper{Key: p.config.HyperKey, VerifyApplication: true, MinimumSensorRounds: 3}).Prepare(ctx, c, target, body)
	})
	if err != nil {
		return body, target, err
	}
	verified, err := checkBookingRecaptcha(ctx, c, target, solution.Token)
	if err != nil {
		return body, target, err
	}
	imageCode := ""
	if verified.Success != "true" {
		u, e := url.Parse(f.captchaImage)
		if e != nil || u.Scheme != "https" || u.Host != "booking.evaair.com" || u.User != nil || !strings.EqualFold(u.Path, "/flyeva/BotDetectCaptcha.ashx") || u.Query().Get("get") != "image" {
			return body, target, award.Failure("captcha_changed", "EVA booking confirmation image destination changed")
		}
		response, image, e := c.Do(ctx, "GET", u.String(), nil, http.Header{"referer": {target}, "sec-fetch-site": {"same-origin"}, "sec-fetch-mode": {"no-cors"}, "sec-fetch-dest": {"image"}})
		if e != nil {
			return body, target, e
		}
		if response == nil || !strings.HasPrefix(response.Header.Get("Content-Type"), "image/") {
			return body, target, award.Failure("captcha_changed", "EVA booking confirmation image unavailable")
		}
		captureAwardImage(image)
		imageCode, err = solver.SolveText(ctx, target, image)
		if err != nil {
			return body, target, err
		}
	}
	if err = prepareCashConfirmation(&f, verified, imageCode); err != nil {
		return body, target, err
	}
	return navigate(ctx, c, "POST", f.action, f.values, target)
}
