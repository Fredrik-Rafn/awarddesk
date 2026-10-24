package eva

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"awarddesk/internal/award"
)

const confirmationFixture = `<form id="PostForm" method="POST" action="https://booking.evaair.com/flyeva/EVA/B2C/booking-confirm.aspx?lang=en-global"><input name="message" value="TRANSBLOCK"><input name="normal_message" value="fresh-notice"><input name="data" value="fresh+state&amp;value"><input name="transblock_pass" value="fresh-pass"></form>`

func TestCashConfirmationPreparesObservedPostbackAndVerifiedCaptcha(t *testing.T) {
	for _, success := range []string{"true", "false"} {
		f := form{values: url.Values{"__EVENTTARGET": {""}, "__EVENTARGUMENT": {""}, "ctl00$content$tb_Captcha": {""}, "ctl00$content$hi_RecaptchaResult": {""}, "ctl00$content$hi_RecaptchaData": {""}, "__ZIPSTATE": {"fresh-state"}}}
		if err := prepareCashConfirmation(&f, bookingRecaptchaResult{Success: success, Source: "fresh-source"}, "ABC123"); err != nil {
			t.Fatal(err)
		}
		code := "******"
		if success == "false" {
			code = "ABC123"
		}
		if f.values.Get("__EVENTTARGET") != "ctl00$content$btn_Confirm" || f.values.Get("ctl00$content$tb_Captcha") != code || f.values.Get("__ZIPSTATE") != "fresh-state" || f.values.Get("ctl00$content$hi_RecaptchaData") != "fresh-source" {
			t.Fatal("confirmation fields differ from the browser postback")
		}
	}
}

func TestCashConfirmationRejectsInvalidVerification(t *testing.T) {
	for _, check := range []struct {
		result bookingRecaptchaResult
		code   string
	}{{bookingRecaptchaResult{Success: "true"}, ""}, {bookingRecaptchaResult{Success: "other", Source: "source"}, ""}, {bookingRecaptchaResult{Success: "false", Source: "source"}, "123"}, {bookingRecaptchaResult{Success: "false", Source: "source"}, "***123"}} {
		f := form{values: url.Values{}}
		if prepareCashConfirmation(&f, check.result, check.code) == nil {
			t.Fatal("invalid confirmation accepted")
		}
	}
}

func TestCashConfirmationAcceptsImageCodesWithinFieldLimit(t *testing.T) {
	for _, code := range []string{"AB12", "AB123", "AB1234"} {
		f := form{values: url.Values{"__EVENTTARGET": {""}, "__EVENTARGUMENT": {""}, "ctl00$content$tb_Captcha": {""}, "ctl00$content$hi_RecaptchaResult": {""}, "ctl00$content$hi_RecaptchaData": {""}}}
		if err := prepareCashConfirmation(&f, bookingRecaptchaResult{Success: "false", Source: "source"}, code); err != nil {
			t.Fatal("valid image code was rejected", len(code))
		}
	}
}

func TestCashConfirmationMatchesChromeBotDetectInitialization(t *testing.T) {
	f := form{values: url.Values{"__EVENTTARGET": {""}, "__EVENTARGUMENT": {""}, "ctl00$content$tb_Captcha": {""}, "ctl00$content$hi_RecaptchaResult": {""}, "ctl00$content$hi_RecaptchaData": {""}, "BDC_BackWorkaround_control": {"0"}, "BDC_VCID_control": {"fresh-image-id"}}}
	if err := prepareCashConfirmation(&f, bookingRecaptchaResult{Success: "false", Source: "source"}, "123456"); err != nil {
		t.Fatal(err)
	}
	if f.values.Get("BDC_BackWorkaround_control") != "1" || f.values.Get("BDC_VCID_control") != "fresh-image-id" {
		t.Fatal("BotDetect initialization differs from Chrome or image identity changed")
	}
}

func TestCashConfirmationUsesOnlyFreshFormOnce(t *testing.T) {
	calls := 0
	response := []byte(`<form action="https://book.evaair.com/plnext/EVAOnlineDX/Override.action"></form>`)
	body, target, err := continueCashConfirmation(context.Background(), []byte(confirmationFixture), BookingURL, func(_ context.Context, action string, values url.Values, from string) ([]byte, string, error) {
		calls++
		if action != "https://booking.evaair.com/flyeva/EVA/B2C/booking-confirm.aspx?lang=en-global" || from != BookingURL || !reflect.DeepEqual(values, url.Values{"message": {"TRANSBLOCK"}, "normal_message": {"fresh-notice"}, "data": {"fresh+state&value"}, "transblock_pass": {"fresh-pass"}}) {
			t.Fatal("confirmation did not preserve its fresh destination, state and referring page")
		}
		return response, action, nil
	})
	if err != nil || calls != 1 || string(body) != string(response) || !strings.Contains(target, "booking-confirm.aspx") {
		t.Fatal("confirmation was not followed exactly once")
	}
}

func TestCashConfirmationRejectsUnrecognizedOrUnsafeForms(t *testing.T) {
	for name, body := range map[string]string{
		"ordinary":  `<form action="https://book.evaair.com/plnext/EVAOnlineDX/Override.action"></form>`,
		"message":   strings.Replace(confirmationFixture, "TRANSBLOCK", "BOOK", 1),
		"host":      strings.Replace(confirmationFixture, "https://booking.evaair.com/flyeva", "https://example.com/flyeva", 1),
		"scheme":    strings.Replace(confirmationFixture, "https://booking", "http://booking", 1),
		"userinfo":  strings.Replace(confirmationFixture, "https://booking", "https://user@booking", 1),
		"path":      strings.Replace(confirmationFixture, "booking-confirm.aspx", "passenger.aspx", 1),
		"method":    strings.Replace(confirmationFixture, `method="POST"`, `method="GET"`, 1),
		"missing":   strings.Replace(confirmationFixture, `value="fresh-pass"`, `value=""`, 1),
		"duplicate": strings.Replace(confirmationFixture, "</form>", `<input name="data" value="another"></form>`, 1),
		"multiple":  confirmationFixture + `<form></form>`,
	} {
		t.Run(name, func(t *testing.T) {
			got, target, err := continueCashConfirmation(context.Background(), []byte(body), BookingURL, func(context.Context, string, url.Values, string) ([]byte, string, error) {
				t.Fatal("unexpected continuation")
				return nil, "", nil
			})
			if err != nil || string(got) != body || target != BookingURL {
				t.Fatal("unrecognized form was changed")
			}
		})
	}
	if _, ok := cashConfirmationForm([]byte(confirmationFixture), "https://example.com/page"); ok {
		t.Fatal("untrusted referring page accepted")
	}
}

func TestCashConfirmationDoesNotReplayFailuresOrLoop(t *testing.T) {
	for _, failure := range []error{nil, context.DeadlineExceeded} {
		calls := 0
		_, _, err := continueCashConfirmation(context.Background(), []byte(confirmationFixture), BookingURL, func(context.Context, string, url.Values, string) ([]byte, string, error) {
			calls++
			return []byte(confirmationFixture), BookingURL, failure
		})
		if calls != 1 || !errors.Is(err, failure) {
			t.Fatal("confirmation was replayed or error lost")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := continueCashConfirmation(ctx, []byte(confirmationFixture), BookingURL, func(context.Context, string, url.Values, string) ([]byte, string, error) {
		t.Fatal("cancelled confirmation posted")
		return nil, "", nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was lost")
	}
}

func TestCashConfirmationRetryUsesReturnedStateAndStopsAfterKnownRejections(t *testing.T) {
	target := "https://booking.evaair.com/flyeva/EVA/B2C/booking-confirm.aspx?lang=en-global"
	page := `<form method="post" action="` + target + `"><input name="__EVENTTARGET"><input name="__EVENTARGUMENT"><input name="__ZIPSTATE" value="first"><input name="ctl00$content$tb_Captcha"><input name="ctl00$content$hi_RecaptchaResult"><input name="ctl00$content$hi_RecaptchaData"></form><script>grecaptcha.execute("public-test-key", { action: "BookingOnline" })</script>`
	rejected := strings.Replace(page, `value="first"`, `value="second"`, 1) + `<div id="content_wuc_Error">Incorrect verification code. Please enter again.</div>`
	calls, recovered := 0, 0
	body, _, err := retryCashConfirmation(context.Background(), []byte(page), target, func(pr award.Progress) {
		if pr.Recovery != nil {
			recovered++
		}
	}, func(_ context.Context, _ []byte, _ string, f form, _ int) ([]byte, string, error) {
		calls++
		want := "first"
		if calls > 1 {
			want = "second"
		}
		if f.values.Get("__ZIPSTATE") != want {
			t.Fatal("retry reused old form state")
		}
		if calls == 1 {
			return []byte(rejected), target, nil
		}
		return []byte("availability handoff"), target, nil
	})
	if err != nil || calls != 2 || recovered != 1 || string(body) != "availability handoff" {
		t.Fatal("explicit rejection did not recover once")
	}
	calls = 0
	_, _, err = retryCashConfirmation(context.Background(), []byte(page), target, nil, func(context.Context, []byte, string, form, int) ([]byte, string, error) {
		calls++
		return []byte(rejected), target, nil
	})
	var failure *award.Error
	if calls != 3 || !errors.As(err, &failure) || failure.Code != "captcha_incorrect" {
		t.Fatal("captcha retries were not bounded")
	}
	calls = 0
	_, _, err = retryCashConfirmation(context.Background(), []byte(page), target, nil, func(context.Context, []byte, string, form, int) ([]byte, string, error) {
		calls++
		return nil, target, context.DeadlineExceeded
	})
	if calls != 1 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("uncertain failure was replayed")
	}
}
