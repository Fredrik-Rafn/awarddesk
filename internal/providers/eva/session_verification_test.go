package eva

import (
	"awarddesk/internal/award"
	"context"
	"errors"
	"testing"
)

func TestSavedSessionVerificationRecoversExplicitRejection(t *testing.T) {
	calls, notices := 0, 0
	body, target, err := verifySavedSession(context.Background(), func() ([]byte, string, error) {
		calls++
		if calls == 1 {
			return nil, ItineraryURL, &award.Error{Code: "session_denied", HTTPStatus: 403}
		}
		return []byte("member"), ItineraryURL, nil
	}, func(award.Progress) { notices++ })
	if err != nil || calls != 2 || notices != 1 || string(body) != "member" || target != ItineraryURL {
		t.Fatalf("calls=%d notices=%d err=%v", calls, notices, err)
	}
}

func TestSavedSessionRepeatedFailureUsesBoundedFreshSessionRecovery(t *testing.T) {
	calls, resets := 0, 0
	err := sessionAttempts(func() error {
		_, _, err := verifySavedSession(context.Background(), func() ([]byte, string, error) {
			calls++
			return nil, ItineraryURL, award.Failure("connection", "interrupted")
		}, nil)
		return err
	}, func() error { resets++; return nil }, nil)
	var failure *award.Error
	if !errors.As(err, &failure) || failure.Code != "connection" || calls != 4 || resets != 1 {
		t.Fatalf("calls=%d resets=%d err=%v", calls, resets, err)
	}
}

func TestSavedSessionDoesNotRetryNontransientOrUncertainFailures(t *testing.T) {
	for _, failure := range []error{
		&award.Error{Code: "session_denied", HTTPStatus: 403, OutcomeUnknown: true},
		award.Failure("session_denied", "no explicit status"),
		award.Failure("authentication", "bad credentials"),
		award.Failure("challenge_service", "access denied"),
		context.Canceled,
	} {
		calls := 0
		_, _, err := verifySavedSession(context.Background(), func() ([]byte, string, error) { calls++; return nil, ItineraryURL, failure }, nil)
		if calls != 1 || err != failure {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := verifySavedSession(ctx, func() ([]byte, string, error) { t.Fatal("called after cancellation"); return nil, "", nil }, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
