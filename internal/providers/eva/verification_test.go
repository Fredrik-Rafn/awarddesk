package eva

import (
	"context"
	"errors"
	"testing"
	"time"

	"awarddesk/internal/antibot"
)

func TestBookingVerificationOverlapsAndWaitsForBoth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	solverStarted, securityStarted := make(chan struct{}), make(chan struct{})
	solution, err := bookingVerification(ctx, func(ctx context.Context) (antibot.CaptchaSolution, error) {
		close(solverStarted)
		select {
		case <-securityStarted:
			return antibot.CaptchaSolution{Token: "test"}, nil
		case <-ctx.Done():
			return antibot.CaptchaSolution{}, ctx.Err()
		}
	}, func(ctx context.Context) error {
		close(securityStarted)
		select {
		case <-solverStarted:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil || solution.Token != "test" {
		t.Fatalf("solution=%+v err=%v", solution, err)
	}
}

func TestBookingVerificationCancelsOtherTaskAndPreservesFailure(t *testing.T) {
	want := errors.New("solver unavailable")
	_, err := bookingVerification(context.Background(), func(context.Context) (antibot.CaptchaSolution, error) { return antibot.CaptchaSolution{}, want }, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	if err != want {
		t.Fatal(err)
	}
	want = errors.New("security failed")
	_, err = bookingVerification(context.Background(), func(ctx context.Context) (antibot.CaptchaSolution, error) {
		<-ctx.Done()
		return antibot.CaptchaSolution{}, ctx.Err()
	}, func(context.Context) error { return want })
	if err != want {
		t.Fatal(err)
	}
}
