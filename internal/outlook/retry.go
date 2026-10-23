package outlook

import (
	"context"
	"errors"
	"io"
	"net"
	"time"
)

type temporaryMailboxError struct{ message string }

func (e *temporaryMailboxError) Error() string { return e.message }

func mailboxReadError(message string, cause error) error {
	var network net.Error
	if errors.As(cause, &network) || errors.Is(cause, io.EOF) || errors.Is(cause, io.ErrUnexpectedEOF) || errors.Is(cause, io.ErrClosedPipe) || errors.Is(cause, net.ErrClosed) || (cause != nil && cause.Error() == "imap: connection closed") {
		return &temporaryMailboxError{message}
	}
	return errors.New(message)
}

func retryMailboxRead[T any](ctx context.Context, read func() (T, error)) (T, error) {
	return retryMailboxReadWithPause(ctx, read, func(ctx context.Context, delay time.Duration) error {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	})
}

func retryMailboxReadWithPause[T any](ctx context.Context, read func() (T, error), pause func(context.Context, time.Duration) error) (T, error) {
	delay := 3 * time.Second
	for {
		var zero T
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		value, err := read()
		var temporary *temporaryMailboxError
		if err == nil || !errors.As(err, &temporary) {
			return value, err
		}
		if err = pause(ctx, delay); err != nil {
			return zero, err
		}
		delay *= 2
		if delay > 15*time.Second {
			delay = 15 * time.Second
		}
	}
}
