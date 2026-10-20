package login

import (
	"awarddesk/internal/award"
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestMaintenancePausesAfterOTPAndFailure(t *testing.T) {
	for _, code := range []string{"mfa_required", "access_denied", "connection"} {
		t.Run(code, func(t *testing.T) {
			var calls atomic.Int32
			s := New(func(context.Context, award.Reporter) error {
				calls.Add(1)
				return award.Failure(code, "synthetic failure")
			})
			defer s.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() { defer close(done); s.maintain(ctx, 5*time.Millisecond) }()
			deadline := time.After(time.Second)
			for calls.Load() == 0 {
				select {
				case <-deadline:
					t.Fatal("maintenance did not start")
				case <-time.After(time.Millisecond):
				}
			}
			await(t, s)
			s.mu.Lock()
			s.next = time.Time{}
			s.mu.Unlock()
			time.Sleep(25 * time.Millisecond)
			cancel()
			<-done
			if calls.Load() != 1 {
				t.Fatal("maintenance repeated credentials after failure")
			}
		})
	}
}
