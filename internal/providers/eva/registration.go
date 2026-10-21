package eva

import (
	"awarddesk/internal/antibot"
	"awarddesk/internal/award"
	"awarddesk/internal/outlook"
	"awarddesk/internal/session"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	http "github.com/bogdanfinn/fhttp"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const RegistrationURL = "https://eservice.evaair.com/flyeva/EVA/FFP/register-now.aspx"

type RegistrationProfile struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Password  string `json:"password"`
	Phone     string `json:"phone"`
	Year      string `json:"year"`
	Month     string `json:"month"`
	Day       string `json:"day"`
}

func NewRegistrationProfile() (RegistrationProfile, error) {
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		return RegistrationProfile{}, err
	}
	name := func(prefix string, data []byte) string {
		for _, v := range data {
			prefix += string(rune('a' + v%26))
		}
		return prefix
	}
	return RegistrationProfile{FirstName: name("Alex", b[:3]), LastName: name("Morgan", b[3:6]), Password: "Ev!" + hex.EncodeToString(b[6:14]) + "Z7", Phone: "20255501" + strconv.Itoa(10+int(b[14])%90), Year: strconv.Itoa(1970 + int(b[15])%30), Month: strconv.Itoa(1 + int(b[16])%12), Day: strconv.Itoa(1 + int(b[17])%28)}, nil
}

// Registration checkpoints are private database data. Pending mutations are
// persisted before sending, and never replayed automatically after a restart.
type RegistrationState struct {
	ChallengeBody []byte              `json:"challengeBody,omitempty"`
	ChallengeURL  string              `json:"challengeURL,omitempty"`
	Proxy         string              `json:"proxy"`
	Template      string              `json:"template"`
	Phase         string              `json:"phase"`
	Profile       RegistrationProfile `json:"profile"`
	Checkpoint    outlook.Checkpoint  `json:"checkpoint"`
	HTTP          session.State       `json:"http"`
	MemberNumber  string              `json:"memberNumber,omitempty"`
}

func registrationEvent(f *form, event string) error {
	if err := f.set("__MasterEVENTTARGET", "ctl00$content$"+event); err != nil {
		return err
	}
	return f.set("__MasterEVENTARGUMENT", "undefined")
}

func registrationProfileForm(f *form, email string, p RegistrationProfile) error {
	for suffix, value := range map[string]string{"$txt_email": email, "$txt_pwd": p.Password, "$txt_repwd": p.Password, "$txt_fename": p.FirstName, "$txt_lename": p.LastName, "$wuc_Birthday$Year": p.Year, "$wuc_Birthday$Month": p.Month, "$wuc_Birthday$Day": p.Day, "$txt_MobilePhoneNo": p.Phone, "$hid_Nationality": "GB", "$hid_MobileCountry": "1-US", "$hid_BirthYY": p.Year, "$hid_BirthMM": p.Month, "$hid_BirthDD": p.Day} {
		if err := f.set(suffix, value); err != nil {
			return award.Failure("registration_protocol", "EVA registration profile fields changed")
		}
	}
	for key, value := range map[string]string{"Nationality___Page": "GB", "Mobile___Page": "1-US", "Read___Page": "E", "ctl00$content$wuc_Sex": "M", "ctl00$content$wuc_recPromotion_A": "N", "ctl00$content$wuc_provideInfo_A": "N", "ctl00$content$wuc_Questionnaire_A": "N", "ctl00$content$wuc_StarQuestionnaire_A": "N"} {
		f.values.Set(key, value)
	}
	return registrationEvent(f, "btn_Confirm")
}

func registrationFailure(body []byte, email, password string) error {
	if text := loginFeedback(body, email, password); text != "" {
		return award.Failure("registration_rejected", "EVA registration: "+text)
	}
	return award.Failure("registration_protocol", "EVA did not return the expected registration step")
}

func (p *Provider) registrationCaptcha(ctx context.Context, c *session.Client, proxy, target string, f *form) error {
	if f.key == "" || f.captchaAction != "registernow" {
		return award.Failure("captcha_changed", "EVA registration CAPTCHA configuration changed")
	}
	solution, err := (antibot.AnySolver{Key: p.config.AnyKey, Proxy: proxy, UserAgent: c.UserAgent()}).SolveRecaptchaV3(ctx, target, f.key, f.captchaAction, f.title)
	if err != nil {
		return err
	}
	return f.set("$hid_grecaptcharesponse", solution.Token)
}

// Register uses the caller's exclusive sticky client throughout creation and
// verification. Only the caller may decide to restart a pre-submission failure.
func (p *Provider) Register(ctx context.Context, c *session.Client, proxy string, state *RegistrationState, save func() error, report award.Reporter) error {
	progress := func(stage, message string) {
		if report != nil {
			report(award.Progress{Stage: stage, Message: message})
		}
	}
	persist := func(phase string) error {
		state.Phase = phase
		httpState, err := c.Snapshot()
		if err != nil {
			return err
		}
		state.HTTP = httpState
		return save()
	}
	continueCreation := func(body []byte, page string) ([]byte, string, error) {
		state.ChallengeBody = body
		state.ChallengeURL = page
		if err := persist("challenge_pending"); err != nil {
			return nil, page, err
		}
		return p.resumePortalChallenge(ctx, c, body, page, "/flyeva/EVA/FFP/register-now.aspx", progress, func() error {
			// Once forwarding starts, a restart must poll email instead of
			// replaying the opaque account-creation wrapper.
			state.ChallengeBody = nil
			state.ChallengeURL = ""
			return persist("submit_pending")
		})
	}
	visit := func(method, target string, values url.Values, from ...string) ([]byte, string, error) {
		body, page, err := navigate(ctx, c, method, target, values, from...)
		if err != nil {
			return body, page, err
		}
		if state.Phase == "submit_pending" && bytes.Contains(body, []byte(`name="sec_chlge_forward_wrap"`)) {
			return continueCreation(body, page)
		}
		return p.resumePortalChallenge(ctx, c, body, page, "/flyeva/EVA/FFP/register-now.aspx", progress)
	}
	if state.Phase == "created" {
		return nil
	}
	if state.Phase == "verification_pending" || state.Phase == "email_login_pending" {
		return award.Failure("registration_uncertain", "Verification was interrupted after submission; inspect the account before continuing")
	}
	if state.Phase == "challenge_pending" {
		if len(state.ChallengeBody) == 0 {
			return award.Failure("registration_protocol", "Saved registration challenge is missing")
		}
		_, _, err := continueCreation(state.ChallengeBody, state.ChallengeURL)
		if err != nil && state.Phase == "challenge_pending" {
			return err
		}
		if err = persist("await_email"); err != nil {
			return err
		}
	}
	if state.Phase == "" {
		progress("proxy verification", "Checking a new exclusive sticky proxy")
		if err := (antibot.Hyper{Key: p.config.HyperKey}).VerifyConnection(ctx, c); err != nil {
			return err
		}
		progress("registration page", "Opening EVA registration")
		body, target, err := visit("GET", RegistrationURL, nil)
		if err != nil {
			return err
		}
		f, err := parseForm(body, target)
		if err != nil {
			return err
		}
		// Choose the same English/UK market as the captured registration flow.
		if err = f.set("__MasterEVENTTARGET", "SETMarket"); err != nil {
			return err
		}
		if err = f.set("__MasterEVENTARGUMENT", "18^"); err != nil {
			return err
		}
		if err = (antibot.Hyper{Key: p.config.HyperKey, SkipPixel: true}).Prepare(ctx, c, target, body); err != nil {
			return err
		}
		response, _, err := c.Do(ctx, "POST", f.action, strings.NewReader(f.values.Encode()), http.Header{"content-type": {"application/x-www-form-urlencoded"}, "origin": {"https://eservice.evaair.com"}, "referer": {target}})
		if err != nil {
			return err
		}
		// SETMarket redirects to the public home page. Its cookies are already
		// stored; reopening registration avoids loading the unrelated homepage.
		location, e := url.Parse(response.Header.Get("Location"))
		if e != nil || response.StatusCode != 302 || location.Scheme != "https" || location.Host != "www.evaair.com" || location.Path != "/en-gb/index.html" {
			return award.Failure("registration_market", "EVA market-selection response changed")
		}
		body, target, err = visit("GET", RegistrationURL, nil, target)
		if err != nil {
			return err
		}
		f, err = parseForm(body, target)
		if err != nil {
			return err
		}
		if err = registrationProfileForm(&f, p.config.Email, state.Profile); err != nil {
			return err
		}
		progress("profile", "Submitting the generated profile")
		body, target, err = visit("POST", f.action, f.values, target)
		if err != nil {
			return err
		}
		f, err = parseForm(body, target)
		if err != nil || f.field("$hid_grecaptcharesponse") == "" {
			return registrationFailure(body, p.config.Email, state.Profile.Password)
		}
		progress("registration captcha", "Solving EVA registration verification")
		if err = p.registrationCaptcha(ctx, c, proxy, target, &f); err != nil {
			return err
		}
		if err = registrationEvent(&f, "wuc_Recaptcha$btn_grecaptcha"); err != nil {
			return err
		}
		body, target, err = visit("POST", f.action, f.values, target)
		if err != nil {
			return err
		}
		f, err = parseForm(body, target)
		if err != nil {
			return err
		}
		if bytes.Contains(body, []byte(`showBotDetect("ok")`)) {
			f.set("$hid_grecaptcharesponse", "ok")
		}
		if f.values.Get(f.field("$hid_grecaptcharesponse")) != "ok" {
			if f.captchaImage == "" {
				return registrationFailure(body, p.config.Email, state.Profile.Password)
			}
			u, e := url.Parse(f.captchaImage)
			if e != nil || u.Scheme != "https" || u.Host != "eservice.evaair.com" {
				return errors.New("invalid registration CAPTCHA origin")
			}
			res, image, e := c.Do(ctx, "GET", f.captchaImage, nil, http.Header{"referer": {target}, "sec-fetch-site": {"same-origin"}, "sec-fetch-dest": {"image"}, "sec-fetch-mode": {"no-cors"}})
			if e != nil {
				return e
			}
			if !strings.HasPrefix(res.Header.Get("Content-Type"), "image/") {
				return errors.New("registration CAPTCHA image unavailable")
			}
			code, e := (antibot.AnySolver{Key: p.config.AnyKey}).SolveText(ctx, target, image)
			if e != nil {
				return e
			}
			if e = f.set("$txt_CaptchaCode", code); e != nil {
				return e
			}
		}
		point, err := p.config.Mailbox.Checkpoint(ctx)
		if err != nil {
			return err
		}
		state.Checkpoint = point
		f.values.Set("ctl00$content$chk_Agree_1", "True")
		f.values.Set("ctl00$content$chk_Agree_2", "True")
		if err = registrationEvent(&f, "btn_Agree"); err != nil {
			return err
		}
		if err = persist("submit_pending"); err != nil {
			return err
		}
		progress("creating account", "Submitting registration once")
		_, _, err = visit("POST", f.action, f.values, target)
		if err != nil && state.Phase == "challenge_pending" {
			return err
		}
		// A lost response may still have generated the email. Poll it rather
		// than submitting a second registration.
		if err = persist("await_email"); err != nil {
			return err
		}
	}
	progress("verification email", "Waiting for a fresh EVA registration link and code")
	message, err := p.config.Mailbox.WaitRegistration(ctx, state.Checkpoint)
	if err != nil {
		return err
	}
	body, target, err := visit("GET", message.Link, nil)
	if err != nil {
		return err
	}
	f, err := parseForm(body, target)
	if err != nil {
		return err
	}
	if err = f.set("$txt_CertificationCode", message.Code); err != nil {
		return registrationFailure(body, p.config.Email, state.Profile.Password)
	}
	if err = registrationEvent(&f, "btn_Certificate_Confirm"); err != nil {
		return err
	}
	if err = persist("verification_pending"); err != nil {
		return err
	}
	progress("email verification", "Submitting the email verification code once")
	body, target, err = visit("POST", f.action, f.values, target)
	if err != nil {
		return err
	}
	f, err = parseForm(body, target)
	if err != nil {
		return err
	}
	state.MemberNumber = f.values.Get(f.field("$hid_Member"))
	if !regexp.MustCompile(`^[0-9]{10}$`).MatchString(state.MemberNumber) {
		return registrationFailure(body, p.config.Email, state.Profile.Password)
	}
	if err = registrationEvent(&f, "btn_Confirm_Email"); err != nil {
		return err
	}
	if err = persist("email_login_pending"); err != nil {
		return err
	}
	progress("email login", "Enabling the email address as the EVA login")
	body, _, err = visit("POST", f.action, f.values, target)
	if err != nil {
		return err
	}
	if !bytes.Contains(body, []byte("is your log-in account.")) {
		return registrationFailure(body, p.config.Email, state.Profile.Password)
	}
	return persist("created")
}
