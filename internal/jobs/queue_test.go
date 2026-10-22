package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"awarddesk/internal/award"
)

type waitingProvider struct{ acquired, entered, finish chan struct{} }

func (p waitingProvider) Capabilities() award.Capabilities { return fakeProvider{}.Capabilities() }
func (p waitingProvider) DefersExecutionStart() bool       { return true }
func (p waitingProvider) Search(ctx context.Context, q award.Query, report award.Reporter) (award.Result, error) {
	close(p.entered)
	select {
	case <-p.acquired:
	case <-ctx.Done():
		return award.Result{}, ctx.Err()
	}
	award.ExecutionStarted(ctx)
	select {
	case <-p.finish:
		return award.Result{Query: q, Complete: true}, nil
	case <-ctx.Done():
		return award.Result{}, ctx.Err()
	}
}

func TestAccountWaitDoesNotConsumeExecutionBudget(t *testing.T) {
	p := waitingProvider{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	s := New(p)
	defer s.Close()
	s.runBudget = 100 * time.Millisecond
	s.queueWait = 2 * time.Second
	j, err := s.Submit(query())
	if err != nil {
		t.Fatal(err)
	}
	<-p.entered
	time.Sleep(150 * time.Millisecond)
	waiting, _ := s.Get(j.ID)
	if waiting.Status != "queued" || waiting.StartedAt != nil {
		t.Fatal("account wait counted as executing", waiting.Status)
	}
	close(p.acquired)
	await(t, s, j.ID, "running")
	close(p.finish)
	await(t, s, j.ID, "completed")
}

func TestQueueExpiryIsSeparateAndHasTerminalMetadata(t *testing.T) {
	p := waitingProvider{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	s := New(p)
	defer s.Close()
	s.queueWait = 30 * time.Millisecond
	j, err := s.Submit(query())
	if err != nil {
		t.Fatal(err)
	}
	finished := await(t, s, j.ID, "failed")
	if finished.Error == nil || finished.Error.Code != "queue_timeout" || finished.StartedAt != nil || finished.FinishedAt == nil {
		t.Fatal("missing queue timeout metadata", finished)
	}
}

func TestExecutionTimeoutBeginsAfterAccountAcquisition(t *testing.T) {
	p := waitingProvider{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	close(p.acquired)
	s := New(p)
	defer s.Close()
	s.runBudget = 30 * time.Millisecond
	j, err := s.Submit(query())
	if err != nil {
		t.Fatal(err)
	}
	finished := await(t, s, j.ID, "failed")
	if finished.Error == nil || finished.Error.Code != "timeout" || finished.StartedAt == nil || finished.FinishedAt == nil {
		t.Fatal("execution timeout not recorded", finished)
	}
}

func queueQuery(i int) award.Query {
	q := query()
	q.Origin = fmt.Sprintf("%c%c%c", 'A'+rune(i/676%26), 'A'+rune(i/26%26), 'A'+rune(i%26))
	if q.Origin == q.Destination {
		q.Destination = "SFO"
	}
	return q
}

func TestFullQueueStillCoalescesAndCancellationReclaimsCapacity(t *testing.T) {
	entered := make(chan struct{})
	var once sync.Once
	s := New(cachedProvider{fakeProvider{func(ctx context.Context) (award.Result, error) {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return award.Result{}, ctx.Err()
	}}})
	defer s.Close()
	active, err := s.Submit(query())
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	var first Job
	for i := 0; i < maxQueuedSearches; i++ {
		j, e := s.Submit(queueQuery(i))
		if e != nil {
			t.Fatalf("admission %d: %v", i, e)
		}
		if i == 0 {
			first = j
		}
	}
	_, err = s.Submit(queueQuery(maxQueuedSearches))
	var failure *award.Error
	if !errors.As(err, &failure) || failure.Code != "queue_full" || failure.RetryAfterSeconds == 0 {
		t.Fatal("queue overflow missing actionable backpressure", err)
	}
	if duplicate, e := s.Submit(first.Query); e != nil || duplicate.ID != first.ID {
		t.Fatal("full queue rejected duplicate")
	}
	q := query()
	q.Destination = "SFO"
	if cached, e := s.Submit(q); e != nil || cached.Status != "completed" {
		t.Fatal("full queue rejected cached result", e)
	}
	if !s.Cancel(first.ID) {
		t.Fatal("cancel failed")
	}
	if _, err = s.Submit(queueQuery(maxQueuedSearches)); err != nil {
		t.Fatal("cancel did not release queue capacity", err)
	}
	s.Cancel(active.ID)
}

func TestReducingConcurrencyDoesNotLaunchAnotherJobEarly(t *testing.T) {
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	s := NewConcurrent(2, fakeProvider{func(ctx context.Context) (award.Result, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return award.Result{Complete: true}, nil
		case <-ctx.Done():
			return award.Result{}, ctx.Err()
		}
	}})
	defer s.Close()
	for i := 0; i < 2; i++ {
		if _, err := s.Submit(queueQuery(i)); err != nil {
			t.Fatal(err)
		}
	}
	<-entered
	<-entered
	s.EnsureWorkers(1)
	if _, err := s.Submit(queueQuery(2)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
		t.Fatal("lowered limit allowed a third active job")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("queue did not resume after active work drained")
	}
}
