package eva

import (
	"awarddesk/internal/award"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestSearchRenewalNeverReplaysCredentialsOrPricing(t *testing.T) {
	for _, stage := range []string{"flight search", "search captcha", "availability", "booking handoff", "booking CAPTCHA", "miles pricing", "checkout price", "credentials", "otp", "session verification"} {
		want := stage == "flight search" || stage == "search captcha" || stage == "availability" || stage == "booking handoff" || stage == "booking CAPTCHA"
		if got := canRenewSearch(stage, award.Failure("connection", "interrupted")); got != want {
			t.Fatalf("stage=%s renewal=%v", stage, got)
		}
	}
	for _, code := range []string{"session_denied", "challenge_service", "captcha_service", "login_response", "proxy_authentication"} {
		if canRenewSearch("availability", award.Failure(code, "test")) {
			t.Fatalf("renewed non-transport failure %s", code)
		}
	}
	if canRenewSearch("availability", context.Canceled) {
		t.Fatal("cancelled search was restarted")
	}
	for _, stage := range []string{"availability", "credentials", "otp", "miles pricing", "checkout price"} {
		if got := canRenewSearch(stage, award.Failure("response_read", "interrupted")); got != (stage == "availability") {
			t.Fatalf("read failure recovery at %s: %v", stage, got)
		}
	}
}

func TestSearchRejectionRecoversWithoutRenewingMemberSession(t *testing.T) {
	calls, notices := 0, 0
	err := retrySearchPreparation(context.Background(), func() (string, error) {
		calls++
		if calls == 1 {
			return "availability", &award.Error{Code: "session_denied", HTTPStatus: 403}
		}
		return "miles pricing", nil
	}, func(p award.Progress) {
		if p.Stage != "search recovery" {
			t.Fatalf("unexpected progress: %s", p.Stage)
		}
		notices++
	})
	if err != nil || calls != 2 || notices != 1 {
		t.Fatalf("calls=%d notices=%d err=%v", calls, notices, err)
	}
}

func TestSearchRejectionRetryBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, stage string
		failure     error
		want        int
	}{
		{"repeated rejection", "availability", &award.Error{Code: "session_denied", HTTPStatus: 403}, 3},
		{"handoff rejection", "booking handoff", &award.Error{Code: "session_denied", HTTPStatus: 403}, 3},
		{"credentials", "credentials", &award.Error{Code: "session_denied", HTTPStatus: 403}, 1},
		{"otp", "otp", &award.Error{Code: "session_denied", HTTPStatus: 403}, 1},
		{"pricing", "miles pricing", &award.Error{Code: "session_denied", HTTPStatus: 403}, 1},
		{"checkout", "checkout price", &award.Error{Code: "session_denied", HTTPStatus: 403}, 1},
		{"solver access denied", "booking security", &award.Error{Code: "challenge_service", HTTPStatus: 403}, 1},
		{"uncertain outcome", "availability", &award.Error{Code: "session_denied", HTTPStatus: 403, OutcomeUnknown: true}, 1},
		{"transport", "availability", award.Failure("connection", "interrupted"), 1},
		{"success", "availability", nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := retrySearchPreparation(context.Background(), func() (string, error) {
				calls++
				return tc.stage, tc.failure
			}, nil)
			if calls != tc.want || err != tc.failure {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestSearchRejectionCancellationStopsRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := retrySearchPreparation(ctx, func() (string, error) {
		calls++
		return "availability", &award.Error{Code: "session_denied", HTTPStatus: 403}
	}, func(award.Progress) { cancel() })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func securityPage(wrapper string) []byte {
	return []byte(fmt.Sprintf(`<title>Pardon Our Interruption</title><form action="%s"><input name="reese84-resubmit-data" value="%s"><input name="must-not-forward" value="private"></form>`, overrideURL, wrapper))
}

func TestBookingSecurityUsesFreshContinuations(t *testing.T) {
	solved, forwarded := 0, 0
	body, target, err := completeBookingSecurity(context.Background(), securityPage("first"), overrideURL,
		func(context.Context, string, []byte) error { solved++; return nil },
		func(_ context.Context, target string, values url.Values, from string) ([]byte, string, error) {
			forwarded++
			want := "first"
			if forwarded == 2 {
				want = "second"
			}
			if target != overrideURL || from != overrideURL || len(values) != 1 || values.Get("reese84-resubmit-data") != want {
				t.Fatal("replayed old wrapper, copied fields, or changed destination")
			}
			if forwarded == 1 {
				return []byte(strings.ReplaceAll(bookingCaptchaFixture, "fresh-wrapper", "second")), target, nil
			}
			return []byte("application"), target, nil
		}, func(string, string) {})
	if err != nil || string(body) != "application" || target != overrideURL || solved != 2 || forwarded != 2 {
		t.Fatalf("solve=%d forward=%d err=%v", solved, forwarded, err)
	}
}

func TestBookingSecurityBoundsAttemptsAndDoesNotReplayErrors(t *testing.T) {
	for _, tc := range []struct {
		name         string
		body         []byte
		forwardError error
		calls        int
	}{
		{"repeated challenge", securityPage("fresh"), nil, 2},
		{"unknown continuation", []byte(`<title>Pardon Our Interruption</title><form action="https://other.test"><input name="reese84-resubmit-data" value="private"></form>`), nil, 0},
		{"uncertain transport", securityPage("fresh"), errors.New("connection lost"), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			_, _, err := completeBookingSecurity(context.Background(), tc.body, overrideURL,
				func(context.Context, string, []byte) error { return nil },
				func(context.Context, string, url.Values, string) ([]byte, string, error) {
					calls++
					return securityPage("new"), overrideURL, tc.forwardError
				},
				func(string, string) {})
			if err == nil || calls != tc.calls {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestBookingSecurityDoesNotSolveAnApplicationPage(t *testing.T) {
	body, _, err := completeBookingSecurity(context.Background(), []byte("application"), overrideURL,
		func(context.Context, string, []byte) error { t.Fatal("unexpected solver call"); return nil },
		func(context.Context, string, url.Values, string) ([]byte, string, error) {
			t.Fatal("unexpected forward")
			return nil, "", nil
		}, func(string, string) {})
	if err != nil || string(body) != "application" {
		t.Fatal(err)
	}
}
