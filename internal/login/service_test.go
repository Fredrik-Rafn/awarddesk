package login

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"awarddesk/internal/award"
)

func TestConcurrentStartsCoalesceAndReuseTimestampedResult(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	s := New(func(ctx context.Context, report award.Reporter) error {
		calls.Add(1)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	defer s.Close()
	var clients sync.WaitGroup
	for i := 0; i < 1000; i++ {
		clients.Add(1)
		go func() {
			defer clients.Done()
			if err := s.Start(); err != nil {
				t.Error(err)
			}
			_ = s.Status()
		}()
	}
	clients.Wait()
	close(release)
	before := await(t, s)
	if calls.Load() != 1 || before.ID != 1 || before.State != "verified" {
		t.Fatalf("burst created duplicate work: calls=%d status=%+v", calls.Load(), before)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	after := s.Status()
	if after.ID != before.ID || !after.FinishedAt.Equal(before.FinishedAt) || calls.Load() != 1 {
		t.Fatal("recent result was rerun or its timestamp changed")
	}
}

func await(t *testing.T, s *Service) Status {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if status := s.Status(); status.State != "running" {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("login did not finish")
	return Status{}
}
func TestSingleLoginCancellationAndCooldown(t *testing.T) {
	s := New(func(ctx context.Context, report award.Reporter) error {
		report(award.Progress{Message: "Checking"})
		<-ctx.Done()
		return ctx.Err()
	})
	defer s.Close()
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal("overlapping request was not coalesced")
	}
	s.Cancel()
	if got := await(t, s); got.State != "cancelled" {
		t.Fatal(got)
	}
	if err := s.Start(); err == nil {
		t.Fatal("cooldown was ignored")
	}
}
func TestLoginResultAndErrorSanitization(t *testing.T) {
	for _, tc := range []struct {
		err            error
		state, message string
	}{
		{nil, "verified", "EVA session ready and saved for reuse. No flight search performed."},
		{errors.New("private upstream contents"), "failed", "EVA login failed"},
		{award.Failure("mfa_required", "Two-step verification required"), "otp_required", "EVA verification requires attention; check the account's Outlook configuration."},
	} {
		s := New(func(context.Context, award.Reporter) error { return tc.err })
		if err := s.Start(); err != nil {
			t.Fatal(err)
		}
		got := await(t, s)
		s.Close()
		if got.State != tc.state || got.Message != tc.message {
			t.Fatal(got)
		}
		if err := s.Start(); err == nil {
			t.Fatal("closed service started")
		}
	}
	s := New(nil)
	defer s.Close()
	if s.Ready() || s.Start() == nil {
		t.Fatal("unconfigured service accepted login")
	}
}

func TestOTPAllowsManualRetryAfterCooldown(t *testing.T) {
	var calls atomic.Int32
	s := New(func(context.Context, award.Reporter) error {
		calls.Add(1)
		return award.Failure("mfa_required", "OTP required")
	})
	defer s.Close()
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if got := await(t, s); got.State != "otp_required" {
		t.Fatal(got)
	}
	if err := s.Start(); err == nil {
		t.Fatal("OTP cooldown ignored")
	}
	s.mu.Lock()
	s.next = time.Time{}
	s.mu.Unlock()
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	await(t, s)
	if calls.Load() != 2 {
		t.Fatal("OTP permanently blocked the account")
	}
}
