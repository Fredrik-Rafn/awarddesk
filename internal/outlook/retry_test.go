package outlook

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"
)

func TestMailboxReadRetriesTransientFailuresWithoutResubmittingAnything(t *testing.T) {
	calls := 0
	var delays []time.Duration
	value, err := retryMailboxReadWithPause(context.Background(), func() (string, error) {
		calls++
		if calls <= 4 {
			return "", mailboxReadError("Outlook read interrupted", io.ErrUnexpectedEOF)
		}
		return "fresh-code", nil
	}, func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	})
	if err != nil || value != "fresh-code" || calls != 5 || !reflect.DeepEqual(delays, []time.Duration{3 * time.Second, 6 * time.Second, 12 * time.Second, 15 * time.Second}) {
		t.Fatalf("value=%q error=%v calls=%d delays=%v", value, err, calls, delays)
	}
}

func TestMailboxReadDoesNotRetryPermanentErrorsOrEmptyInbox(t *testing.T) {
	for _, failure := range []error{errIMAPAuth, errors.New("Outlook mailbox identity changed during login"), errors.New("Outlook refresh token expired or requires renewed consent"), nil} {
		calls := 0
		_, err := retryMailboxReadWithPause(context.Background(), func() (string, error) {
			calls++
			return "", failure
		}, func(context.Context, time.Duration) error {
			t.Fatal("permanent failure or empty inbox was retried")
			return nil
		})
		if err != failure || calls != 1 {
			t.Fatalf("error=%v calls=%d", err, calls)
		}
	}
}

func TestMailboxReadStopsAtExistingDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := retryMailboxReadWithPause(ctx, func() (string, error) {
		calls++
		return "", &temporaryMailboxError{"Outlook unavailable"}
	}, func(context.Context, time.Duration) error {
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestMailboxErrorClassification(t *testing.T) {
	for _, failure := range []error{io.EOF, io.ErrUnexpectedEOF, io.ErrClosedPipe, errors.New("imap: connection closed")} {
		var temporary *temporaryMailboxError
		if got := mailboxReadError("Outlook request interrupted", failure); !errors.As(got, &temporary) || got.Error() != "Outlook request interrupted" {
			t.Fatalf("transport failure %v was not safely classified", failure)
		}
	}
	var temporary *temporaryMailboxError
	if errors.As(mailboxReadError("Outlook request rejected", errors.New("invalid folder")), &temporary) {
		t.Fatal("permanent rejection classified as transient")
	}
}
