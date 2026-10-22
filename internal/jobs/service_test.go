package jobs

import (
	"awarddesk/internal/award"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProvider struct {
	run func(context.Context) (award.Result, error)
}

type previewProvider struct{ ready, release chan struct{} }

func (p previewProvider) Capabilities() award.Capabilities { return fakeProvider{}.Capabilities() }
func (p previewProvider) Search(ctx context.Context, q award.Query, report award.Reporter) (award.Result, error) {
	r := award.Result{Query: q, Warnings: []string{}, Itineraries: []award.Itinerary{{ID: "flight", Fares: []award.Fare{{CashPrice: &award.Money{Amount: "100"}, MilesCash: &award.MilesCash{Miles: 123}}}}}}
	report(award.Progress{Stage: "miles pricing", Preview: &r})
	r.Itineraries[0].Fares[0].CashPrice.Amount = "200"
	r.Itineraries[0].Fares[0].MilesCash.Miles = 456
	close(p.ready)
	select {
	case <-p.release:
		r.Complete = true
		return r, nil
	case <-ctx.Done():
		return r, ctx.Err()
	}
}

func TestRunningJobPublishesImmutablePartialPreview(t *testing.T) {
	p := previewProvider{ready: make(chan struct{}), release: make(chan struct{})}
	s := New(p)
	defer s.Close()
	j, err := s.Submit(query())
	if err != nil {
		t.Fatal(err)
	}
	<-p.ready
	j, _ = s.Get(j.ID)
	if j.Status != "running" || j.Result == nil || j.Result.Complete || j.Progress.Preview != nil || j.Result.Itineraries[0].Fares[0].CashPrice.Amount != "100" || j.Result.Itineraries[0].Fares[0].MilesCash.Miles != 123 {
		t.Fatal("running preview missing, mutable, or complete")
	}
	close(p.release)
	j = await(t, s, j.ID, "completed")
	if !j.Result.Complete || j.Result.Itineraries[0].Fares[0].MilesCash.Miles != 456 {
		t.Fatal("final result did not replace preview")
	}
}

func (p fakeProvider) Capabilities() award.Capabilities {
	return award.Capabilities{ID: "test", Cabins: []award.Cabin{award.Economy}, MaxAdults: 9}
}
func (p fakeProvider) Search(ctx context.Context, _ award.Query, _ award.Reporter) (award.Result, error) {
	return p.run(ctx)
}
func query() award.Query {
	return award.Query{Provider: "test", Origin: "JFK", Destination: "LAX", Date: time.Now().AddDate(0, 0, 1).Format("2006-01-02"), Cabin: award.Economy, Adults: 1}
}
func await(t *testing.T, s *Service, id, status string) Job {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	for time.Now().Before(end) {
		j, _ := s.Get(id)
		if j.Status == status {
			return j
		}
		time.Sleep(time.Millisecond)
	}
	j, _ := s.Get(id)
	t.Fatalf("expected %s, got %+v", status, j)
	return Job{}
}
func TestSequentialJobsAndCancellation(t *testing.T) {
	var active, peak atomic.Int32
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	s := New(fakeProvider{func(ctx context.Context) (award.Result, error) {
		n := active.Add(1)
		defer active.Add(-1)
		if n > peak.Load() {
			peak.Store(n)
		}
		entered <- struct{}{}
		select {
		case <-ctx.Done():
			return award.Result{}, ctx.Err()
		case <-release:
			return award.Result{Complete: true}, nil
		}
	}})
	defer s.Close()
	a, e := s.Submit(query())
	if e != nil {
		t.Fatal(e)
	}
	<-entered
	otherQuery := query()
	otherQuery.Destination = "SFO"
	b, e := s.Submit(otherQuery)
	if e != nil {
		t.Fatal(e)
	}
	s.Cancel(b.ID)
	s.Cancel(a.ID)
	await(t, s, a.ID, "cancelled")
	await(t, s, b.ID, "cancelled")
	c, _ := s.Submit(query())
	<-entered
	close(release)
	await(t, s, c.ID, "completed")
	if peak.Load() != 1 {
		t.Fatal("concurrent account use")
	}
}
func TestRateLimitCooldownAndSanitizedErrors(t *testing.T) {
	s := New(fakeProvider{func(context.Context) (award.Result, error) {
		return award.Result{}, &award.Error{Code: "rate_limited", Message: "pause", RetryAfterSeconds: 60}
	}})
	defer s.Close()
	a, _ := s.Submit(query())
	await(t, s, a.ID, "failed")
	if _, e := s.Submit(query()); e == nil {
		t.Fatal("cooldown not enforced")
	}
	other := New(fakeProvider{func(context.Context) (award.Result, error) { return award.Result{}, errors.New("secret credential") }})
	defer other.Close()
	b, _ := other.Submit(query())
	j := await(t, other, b.ID, "failed")
	if j.Error.Message == "secret credential" {
		t.Fatal("raw error exposed")
	}
}

func TestLaterPricingFailureKeepsPartialFlightData(t *testing.T) {
	s := New(fakeProvider{func(context.Context) (award.Result, error) {
		return award.Result{Query: query(), Complete: true, Itineraries: []award.Itinerary{{ID: "flight", Fares: []award.Fare{{MilesCash: &award.MilesCash{Miles: 12345}}}}}}, award.Failure("search_protocol", "Pricing response missing")
	}})
	defer s.Close()
	j, err := s.Submit(query())
	if err != nil {
		t.Fatal(err)
	}
	j = await(t, s, j.ID, "failed")
	if j.Error == nil || j.Result == nil || j.Result.Complete || j.Result.Itineraries[0].Fares[0].MilesCash.Miles != 12345 {
		t.Fatal("late failure discarded quotes or became a complete success")
	}
}

func TestIdenticalInflightSearchesShareOneJob(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s := New(fakeProvider{func(ctx context.Context) (award.Result, error) {
		calls.Add(1)
		close(entered)
		select {
		case <-release:
			return award.Result{Complete: true}, nil
		case <-ctx.Done():
			return award.Result{}, ctx.Err()
		}
	}})
	defer s.Close()
	a, err := s.Submit(query())
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	for i := 0; i < 100; i++ {
		b, err := s.Submit(query())
		if err != nil || b.ID != a.ID {
			t.Fatalf("duplicate queued: id=%s err=%v", b.ID, err)
		}
	}
	close(release)
	await(t, s, a.ID, "completed")
	if calls.Load() != 1 {
		t.Fatal("duplicate upstream work")
	}
}

type cachedProvider struct{ fakeProvider }

type recoveryProvider struct{ failure error }

func (p recoveryProvider) Capabilities() award.Capabilities { return fakeProvider{}.Capabilities() }
func (p recoveryProvider) Search(_ context.Context, q award.Query, report award.Reporter) (award.Result, error) {
	for attempt := 1; attempt <= 2; attempt++ {
		report(award.Progress{Stage: "recovery", Message: "retrying", Recovery: &award.Recovery{Stage: "availability", Code: "session_denied", Attempt: attempt}})
	}
	return award.Result{Query: q, Complete: p.failure == nil}, p.failure
}

func TestRecoveryHistorySurvivesCompletionAndFailure(t *testing.T) {
	for _, failure := range []error{nil, award.Failure("connection", "interrupted")} {
		s := New(recoveryProvider{failure})
		defer s.Close()
		j, err := s.Submit(query())
		if err != nil {
			t.Fatal(err)
		}
		want := "completed"
		if failure != nil {
			want = "failed"
		}
		finished := await(t, s, j.ID, want)
		if finished.StartedAt == nil || finished.FinishedAt == nil || finished.StartedAt.Before(finished.CreatedAt) || finished.FinishedAt.Before(*finished.StartedAt) || len(finished.Timeline) != 2 {
			t.Fatal("search timing diagnostics missing or out of order")
		}
		finished.Timeline[0].Stage = "modified snapshot"
		copy, _ := s.Get(j.ID)
		if copy.Timeline[0].Stage == "modified snapshot" { t.Fatal("timeline snapshot mutates live job") }
		if len(finished.Recoveries) != 2 || finished.Recoveries[0].Code != "session_denied" || finished.Recoveries[1].Attempt != 2 {
			t.Fatalf("recovery history lost: %+v", finished.Recoveries)
		}
	}
}

func (p cachedProvider) CachedSearch(q award.Query) (award.Result, bool) {
	return award.Result{Query: q, Complete: true, Cached: true, FetchedAt: time.Now()}, q.Destination == "SFO" && !q.Refresh
}

func TestCachedSearchBypassesBusyLiveQueue(t *testing.T) {
	entered := make(chan struct{})
	s := New(cachedProvider{fakeProvider{func(ctx context.Context) (award.Result, error) {
		close(entered)
		<-ctx.Done()
		return award.Result{}, ctx.Err()
	}}})
	defer s.Close()
	_, err := s.Submit(query())
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	q := query()
	q.Destination = "SFO"
	j, err := s.Submit(q)
	if err != nil || j.Status != "completed" || j.Result == nil || !j.Result.Cached {
		t.Fatalf("cache waited for provider: %+v err=%v", j, err)
	}
}

func TestClosedAndExpiredJobs(t *testing.T) {
	s := New(fakeProvider{func(context.Context) (award.Result, error) { return award.Result{}, nil }})
	j, e := s.Submit(query())
	if e != nil {
		t.Fatal(e)
	}
	await(t, s, j.ID, "completed")
	s.mu.Lock()
	s.items[j.ID].UpdatedAt = time.Now().Add(-2 * time.Hour)
	s.mu.Unlock()
	if _, ok := s.Get(j.ID); ok {
		t.Fatal("expired result returned")
	}
	s.Close()
	if _, e = s.Submit(query()); e == nil {
		t.Fatal("closed queue accepted work")
	}
}
