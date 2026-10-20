package ana

import (
	"awarddesk/internal/award"
	"context"
	"errors"
	"testing"
)

func TestRetryBoundary(t *testing.T) {
	for _, tc := range []struct {
		stage, code     string
		submitted, want bool
	}{
		{"session setup", "connection", false, true},
		{"security initialization", "connection", false, true},
		{"authorization", "response_read", false, true},
		{"credentials", "connection", true, false},
		{"member verification", "connection", true, false},
		{"credentials", "authentication", true, false},
		{"security initialization", "rate_limited", false, false},
		{"authorization", "proxy_authentication", false, false},
	} {
		err := loginFailure(award.Failure(tc.code, "safe failure"), tc.stage, tc.submitted)
		if retryBeforeCredentials(err) != tc.want {
			t.Fatalf("wrong retry decision for %s/%s", tc.stage, tc.code)
		}
		var ae *award.Error
		if !errors.As(err, &ae) || ae.Stage != tc.stage {
			t.Fatal("failure stage lost")
		}
		if tc.submitted && tc.code == "connection" && !ae.OutcomeUnknown {
			t.Fatal("uncertain credential result not marked")
		}
	}
}
func TestAccountConcurrencyBound(t *testing.T) {
	p := New(Config{MemberNumber: "0000000000", Password: "synthetic-password"})
	p.gate <- struct{}{}
	_, err := p.Login(context.Background(), nil)
	var ae *award.Error
	if !errors.As(err, &ae) || ae.Code != "busy" {
		t.Fatal("overlapping account login accepted")
	}
	<-p.gate
}
