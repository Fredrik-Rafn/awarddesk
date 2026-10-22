package eva

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"awarddesk/internal/award"
)

func TestImageRetryRequiresExplicitRejectionAndReturnedLoginForm(t *testing.T) {
	withImage := form{captchaImage: "https://eservice.evaair.com/fresh-image"}
	member := form{values: url.Values{"ctl00$content$txt_Member": {""}}}
	otp := form{values: url.Values{"ctl00$content$txt_CertificationCode": {""}}}
	for _, tc := range []struct {
		original, next form
		feedback       string
		retry          bool
	}{
		{withImage, member, "Wrong CAPTCHA entry. Please try again.", true},
		{form{}, member, "Wrong CAPTCHA entry. Please try again.", false},
		{withImage, otp, "Wrong CAPTCHA entry. Please try again.", false},
		{withImage, member, "Wrong password. Please try again.", false},
		{withImage, member, "", false},
	} {
		if got := imageCaptchaRejected(tc.original, tc.next, tc.feedback); got != tc.retry {
			t.Fatal("incorrect credential-retry classification")
		}
	}
}

func TestSessionRecoveryBudgetsAndUncertainLoginOutcomes(t *testing.T) {
	broken := &searchRenewal{cause: award.Failure("connection", "search preparation interrupted")}
	captcha := award.Failure("captcha_incorrect", "explicit rejection")
	for _, tc := range []struct {
		name          string
		outcomes      []error
		calls, resets int
		success       bool
	}{
		{"expired session", []error{errExpired, nil}, 2, 0, true},
		{"interrupted search", []error{broken, nil}, 2, 1, true},
		{"image rejected", []error{captcha, nil}, 2, 0, true},
		{"expiry and image rejection", []error{errExpired, captcha, nil}, 3, 0, true},
		{"interruption and image rejection", []error{broken, captcha, nil}, 3, 1, true},
		{"repeated image rejection", []error{captcha, captcha}, 2, 0, false},
		{"repeated connection failure", []error{broken, broken}, 2, 1, false},
		{"repeated expiry", []error{errExpired, errExpired}, 2, 0, false},
		{"uncertain credential outcome", []error{award.Failure("connection", "credentials interrupted")}, 1, 0, false},
		{"denied login", []error{award.Failure("session_denied", "denied")}, 1, 0, false},
		{"wrong password", []error{award.Failure("login_response", "wrong password")}, 1, 0, false},
		{"OTP failure", []error{award.Failure("otp", "OTP failed")}, 1, 0, false},
		{"cancelled", []error{context.Canceled}, 1, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, resets := 0, 0
			err := sessionAttempts(func() error {
				if calls >= len(tc.outcomes) {
					t.Fatal("extra attempt")
				}
				e := tc.outcomes[calls]
				calls++
				return e
			}, func() error { resets++; return nil }, nil)
			if calls != tc.calls || resets != tc.resets || (err == nil) != tc.success {
				t.Fatalf("calls=%d resets=%d err=%v", calls, resets, err)
			}
			if tc.name == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation was replaced")
			}
		})
	}
}
