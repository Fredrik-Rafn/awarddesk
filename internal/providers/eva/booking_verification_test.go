package eva

import (
	"context"
	"errors"
	"io"
	"net/url"
	"testing"

	http "github.com/bogdanfinn/fhttp"

	"awarddesk/internal/antibot"
)

type bookingSolverFunc func(context.Context, string, string) (antibot.CaptchaSolution, error)

func (f bookingSolverFunc) Solve(c context.Context, page, key string) (antibot.CaptchaSolution, error) {
	return f(c, page, key)
}

type bookingRequestFunc func(context.Context, string, string, io.Reader, http.Header) (*http.Response, []byte, error)

func (f bookingRequestFunc) Do(c context.Context, method, target string, body io.Reader, h http.Header) (*http.Response, []byte, error) {
	return f(c, method, target, body, h)
}

func TestBookingCaptchaVerifiesTokenBeforeForwardingWrapper(t *testing.T) {
	for _, failVerification := range []bool{false, true} {
		verified, forwarded := 0, 0
		client := bookingRequestFunc(func(_ context.Context, method, target string, body io.Reader, h http.Header) (*http.Response, []byte, error) {
			data, _ := io.ReadAll(body)
			values, _ := url.ParseQuery(string(data))
			if method != "POST" || target != "https://book.evaair.com/_Incapsula_Resource?SWCGHOEL=v2&dai=fresh-id&cts=fresh-time" || len(values) != 1 || values.Get("g-recaptcha-response") != "solved-token" || h["origin"][0] != "https://book.evaair.com" || h["referer"][0] != overrideURL {
				t.Fatal("wrong verification request or copied continuation data")
			}
			verified++
			if failVerification {
				return nil, nil, errors.New("verification failed")
			}
			return &http.Response{StatusCode: 200}, nil, nil
		})
		solver := bookingSolverFunc(func(_ context.Context, page, key string) (antibot.CaptchaSolution, error) {
			if page != overrideURL || key != "e94865c2-4231-4c25-9c6e-2b797b2b56cf" {
				t.Fatal("wrong live challenge context")
			}
			return antibot.CaptchaSolution{Token: "solved-token"}, nil
		})
		_, _, err := completeBookingSecurity(context.Background(), []byte(bookingCaptchaFixture), overrideURL,
			func(ctx context.Context, page string, body []byte) error {
				handled, err := verifyBookingCaptcha(ctx, client, solver, body, page, func(string, string) {})
				if !handled {
					t.Fatal("CAPTCHA not handled")
				}
				return err
			},
			func(_ context.Context, target string, values url.Values, from string) ([]byte, string, error) {
				if verified != 1 || target != overrideURL || from != overrideURL || len(values) != 1 || values.Get("reese84-resubmit-data") != "fresh-wrapper" {
					t.Fatal("wrapper forwarded before verification or with extra fields")
				}
				forwarded++
				return []byte("application"), target, nil
			}, func(string, string) {})
		if verified != 1 || (err != nil) != failVerification || (forwarded == 0) != failVerification {
			t.Fatalf("verify=%d forward=%d err=%v", verified, forwarded, err)
		}
	}
}

func TestBookingCaptchaDoesNotTreatRedirectAsSuccessfulVerification(t *testing.T) {
	client := bookingRequestFunc(func(context.Context, string, string, io.Reader, http.Header) (*http.Response, []byte, error) {
		return &http.Response{StatusCode: 302}, nil, nil
	})
	solver := bookingSolverFunc(func(context.Context, string, string) (antibot.CaptchaSolution, error) {
		return antibot.CaptchaSolution{Token: "test-token"}, nil
	})
	handled, err := verifyBookingCaptcha(context.Background(), client, solver, []byte(bookingCaptchaFixture), overrideURL, func(string, string) {})
	if !handled || err == nil {
		t.Fatal("redirect accepted as verified CAPTCHA")
	}
}
