package eva

import (
	"awarddesk/internal/award"
	"context"
	"errors"
	"testing"
)

func TestReeseFallbackOnlyForUpstreamServiceFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failure  error
		fallback bool
	}{
		{"primary success", nil, false},
		{"access denied", award.Failure("challenge_service", "denied"), true},
		{"proxy connection", award.Failure("connection", "interrupted"), false},
		{"airline denial", &award.Error{Code: "session_denied", HTTPStatus: 403}, false},
		{"changed challenge", award.Failure("challenge_changed", "changed"), false},
		{"uncertain", &award.Error{Code: "challenge_service", OutcomeUnknown: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, notices := 0, 0
			backupErr := errors.New("backup outcome")
			err := reeseWithFallback(context.Background(), func(context.Context) error { return tc.failure }, func(context.Context) error { calls++; return backupErr }, func(string, string) { notices++ })
			want := tc.failure
			if tc.fallback {
				want = backupErr
			}
			if err != want || (calls == 1) != tc.fallback || (notices == 1) != tc.fallback {
				t.Fatalf("calls=%d notices=%d err=%v", calls, notices, err)
			}
		})
	}
}

func TestReeseFallbackStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	err := reeseWithFallback(ctx, func(context.Context) error { cancel(); return award.Failure("challenge_service", "interrupted") }, func(context.Context) error { t.Fatal("backup after cancellation"); return nil }, func(string, string) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestReeseFallbackSucceedsOnce(t *testing.T) {
	primary, backup := 0, 0
	err := reeseWithFallback(context.Background(), func(context.Context) error { primary++; return award.Failure("challenge_service", "generation failed") }, func(context.Context) error { backup++; return nil }, func(string, string) {})
	if err != nil || primary != 1 || backup != 1 {
		t.Fatalf("primary=%d backup=%d err=%v", primary, backup, err)
	}
}
