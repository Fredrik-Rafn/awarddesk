package eva

import (
	"awarddesk/internal/award"
	"awarddesk/internal/outlook"
	"awarddesk/internal/proxies"
	"context"
	"errors"
	"testing"
	"time"
)

func TestSearchWaitsForSessionAndHonorsCancellation(t *testing.T) {
	p := New(Config{Email: "test@example.test", Password: "test", Pool: &proxies.Pool{}, Mailbox: &outlook.Reader{}})
	p.gate <- struct{}{}
	defer func() { <-p.gate }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := p.Ensure(ctx, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting search did not honor cancellation: %v", err)
	}
}

func TestSearchFailureIncludesSessionStage(t *testing.T) {
	q := award.Query{Provider: "eva", Origin: "HND", Destination: "SIN", Date: time.Now().AddDate(0, 0, 1).Format("2006-01-02"), Cabin: award.Economy, Adults: 1}
	_, err := New(Config{}).Search(context.Background(), q, nil)
	var failure *award.Error
	if !errors.As(err, &failure) || failure.Stage != "session preparation" || failure.Code != "configuration" {
		t.Fatalf("failure lost its stage or classification: %v", err)
	}
}

func TestProxyRenewalIsRestrictedToConnectionPreflightFailures(t *testing.T) {
	for _, tc := range []struct {
		code  string
		renew bool
	}{{"connection", true}, {"proxy_changed", true}, {"authentication", false}, {"login_response", false}, {"otp", false}, {"challenge_service", false}, {"proxy_authentication", false}} {
		if got := proxyNeedsRenewal(award.Failure(tc.code, "test")); got != tc.renew {
			t.Fatalf("%s renewal=%v", tc.code, got)
		}
	}
}
