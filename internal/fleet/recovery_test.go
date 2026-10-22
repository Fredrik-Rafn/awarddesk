package fleet

import (
	"context"
	"testing"
	"time"

	"awarddesk/internal/award"
)

func TestPreparationRetriesUntilFifteenthFailureThenPauses(t *testing.T) {
	f, providers := setup(t, 1)
	providers["a"].info.Ready = false
	providers["a"].run = func(context.Context) (award.Result, error) {
		return award.Result{}, &award.Error{Code: "session_denied", Stage: "credentials", HTTPStatus: 403}
	}
	for attempt := 1; attempt <= MaxPreparationFailures; attempt++ {
		if err := f.Prepare(context.Background(), "a", nil, true); err == nil {
			t.Fatal("expected login failure")
		}
		var status Status
		f.db.RuntimeValue("account:a", &status)
		if status.ConsecutiveFailures != attempt || status.NeedsAttention != (attempt == MaxPreparationFailures) {
			t.Fatalf("attempt %d: %+v", attempt, status)
		}
		if attempt < MaxPreparationFailures {
			delay := status.NextAttempt.Sub(status.CheckedAt)
			if delay < 15*time.Minute || delay > 30*time.Minute {
				t.Fatalf("invalid retry delay: %s", delay)
			}
		} else if !status.NextAttempt.IsZero() {
			t.Fatal("exhausted account still has automatic retry scheduled")
		}
	}
	providers["a"].run = func(context.Context) (award.Result, error) { return award.Result{Complete: true}, nil }
	if err := f.Prepare(context.Background(), "a", nil, true); err != nil {
		t.Fatal(err)
	}
	var status Status
	f.db.RuntimeValue("account:a", &status)
	if status.ConsecutiveFailures != 0 || status.NeedsAttention || !status.NextAttempt.IsZero() {
		t.Fatal("success did not reset recovery budget")
	}
}

func TestRetryCooldownExcludesAccountUntilDue(t *testing.T) {
	f, providers := setup(t, 1)
	f.outcome("a", providers["a"], &award.Error{Code: "timeout", Stage: "credentials", OutcomeUnknown: true})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, _, err := f.claim(ctx, "a", nil, false)
	if err == nil {
		t.Fatal("uncertain login was immediately replayed")
	}
	var status Status
	f.db.RuntimeValue("account:a", &status)
	status.NextAttempt = time.Now().Add(-time.Second)
	f.db.Runtime("account:a", status)
	_, _, release, err := f.claim(context.Background(), "a", nil, false)
	if err != nil {
		t.Fatalf("due retry did not become eligible: %v", err)
	}
	release()
}

func TestRecoverySurvivesRestartAndUpgradesOldAttentionFlag(t *testing.T) {
	now := time.Now().UTC()
	old := Status{ID: "a", State: "failed", NeedsAttention: true, CheckedAt: now.Add(-time.Hour)}
	updated := recoverPreparationStatus(old, now)
	if updated.NeedsAttention || updated.ConsecutiveFailures != 1 || updated.NextAttempt.IsZero() || updated.NextAttempt.After(now) {
		t.Fatal("old permanent pause was not converted to a due retry")
	}
	if again := recoverPreparationStatus(updated, now.Add(time.Minute)); again.NextAttempt != updated.NextAttempt || again.ConsecutiveFailures != 1 {
		t.Fatal("restart changed existing retry schedule")
	}
	updated.State = "preparing"
	interrupted := recoverPreparationStatus(updated, now)
	if interrupted.ConsecutiveFailures != 2 || interrupted.NextAttempt.Before(now.Add(15*time.Minute)) {
		t.Fatal("interrupted operation was not delayed")
	}
}

func TestServerCooldownIsHonoredBeyondLocalDelay(t *testing.T) {
	f, providers := setup(t, 1)
	f.outcome("a", providers["a"], &award.Error{Code: "rate_limited", RetryAfterSeconds: 7200})
	var status Status
	f.db.RuntimeValue("account:a", &status)
	if status.NextAttempt.Sub(status.CheckedAt) < 2*time.Hour {
		t.Fatal("provider retry-after was shortened")
	}
}

func TestMaintenancePrioritizesMissingSessionsOverHealthyChecks(t *testing.T) {
	f, providers := setup(t, 3)
	f.db.SetSettings(map[string]string{"HYPER_API_KEY": "test", "ANYSOLVER_API_KEY": "test", "PROXY_LIST": "test"})
	release := make(chan struct{})
	defer close(release)
	for id, provider := range providers {
		provider.run = func(ctx context.Context) (award.Result, error) {
			select {
			case <-release:
				return award.Result{}, nil
			case <-ctx.Done():
				return award.Result{}, ctx.Err()
			}
		}
		f.db.Runtime("account:"+id, Status{ID: id, State: "ready", CheckedAt: time.Now().Add(-time.Hour)})
	}
	providers["c"].info.Ready = false
	f.db.Runtime("account:c", Status{ID: "c", CheckedAt: time.Now()})
	f.maintain()
	f.mu.Lock()
	missingQueued := f.pending["c"]
	queued := len(f.pending)
	f.mu.Unlock()
	if !missingQueued || queued != 2 {
		t.Fatalf("expired session lost priority: selected=%t pending=%d", missingQueued, queued)
	}
}

func TestQueuedPreparationPreservesFailureBudget(t *testing.T) {
	f, providers := setup(t, 1)
	providers["a"].run = func(context.Context) (award.Result, error) {
		return award.Result{}, award.Failure("connection", "synthetic failure")
	}
	f.db.Runtime("account:a", Status{ID: "a", State: "failed", ConsecutiveFailures: 4})
	if _, err := f.enqueuePrepare("a", false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var status Status
		f.db.RuntimeValue("account:a", &status)
		if status.State == "failed" {
			if status.ConsecutiveFailures != 5 {
				t.Fatal("queue reset the persistent failure count")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("preparation did not finish")
}
