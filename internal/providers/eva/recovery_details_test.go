package eva

import (
	"awarddesk/internal/award"
	"awarddesk/internal/session"
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRequestRecoveriesKeepCurrentStageAndSerializeReports(t *testing.T) {
	var active, calls atomic.Int32
	var details []award.Recovery
	ctx, report := trackRequestRecoveries(context.Background(), func(p award.Progress) {
		if active.Add(1) != 1 {
			t.Error("concurrent reporter invocation")
		}
		defer active.Add(-1)
		calls.Add(1)
		if p.Recovery != nil {
			details = append(details, *p.Recovery)
		}
	})
	report(award.Progress{Stage: "availability"})
	session.ReportRetry(ctx, award.Failure("response_read", "interrupted"), 1)
	if len(details) != 1 || details[0].Stage != "availability" || details[0].Code != "response_read" {
		t.Fatalf("lost current stage: %+v", details)
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); report(award.Progress{Stage: "booking form security"}) }()
		go func() { defer wg.Done(); session.ReportRetry(ctx, award.Failure("captcha_service", "temporary"), 1) }()
	}
	wg.Wait()
	if calls.Load() != 202 || len(details) != 101 {
		t.Fatalf("lost reports: calls=%d details=%d", calls.Load(), len(details))
	}
}
