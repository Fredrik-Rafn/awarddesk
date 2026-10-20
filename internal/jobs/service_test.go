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
	b, e := s.Submit(query())
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
