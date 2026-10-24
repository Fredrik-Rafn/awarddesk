package jobs

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"awarddesk/internal/award"
)

type activityProvider struct {
	fakeProvider
	submissions atomic.Int32
	cache       bool
}

func (p *activityProvider) SearchSubmitted(at time.Time) {
	if !at.IsZero() {
		p.submissions.Add(1)
	}
}
func (p *activityProvider) CachedSearch(q award.Query) (award.Result, bool) {
	return award.Result{Query: q, Complete: true, FetchedAt: time.Now().UTC(), Cached: true}, p.cache
}

func TestActivityCountsCoalescedSubmissionsButNotInvalidRequestsOrPolling(t *testing.T) {
	entered := make(chan struct{}, 1)
	p := &activityProvider{fakeProvider: fakeProvider{run: func(ctx context.Context) (award.Result, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return award.Result{}, ctx.Err()
	}}}
	s := New(p)
	defer s.Close()
	first, err := s.Submit(query())
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	second, err := s.Submit(query())
	if err != nil || first.ID != second.ID || p.submissions.Load() != 2 {
		t.Fatalf("coalesced activity=%d error=%v", p.submissions.Load(), err)
	}
	_, _ = s.Get(first.ID)
	_ = s.Capabilities()
	bad := query()
	bad.Destination = bad.Origin
	if _, err := s.Submit(bad); err == nil {
		t.Fatal("invalid query accepted")
	}
	bad = query()
	bad.Provider = "unknown"
	if _, err := s.Submit(bad); err == nil {
		t.Fatal("unknown provider accepted")
	}
	bad = query()
	bad.Cabin = award.Business
	if _, err := s.Submit(bad); err == nil {
		t.Fatal("unsupported cabin accepted")
	}
	if p.submissions.Load() != 2 {
		t.Fatal("reads or rejected requests reset activity")
	}
	s.Close()
	_, _ = s.Submit(query())
	if p.submissions.Load() != 2 {
		t.Fatal("closed service reset activity")
	}
}

func TestActivityCountsCachedSearchSubmissions(t *testing.T) {
	p := &activityProvider{cache: true, fakeProvider: fakeProvider{run: func(context.Context) (award.Result, error) {
		t.Error("cache hit performed live work")
		return award.Result{}, nil
	}}}
	s := New(p)
	defer s.Close()
	for i := 0; i < 2; i++ {
		job, err := s.Submit(query())
		if err != nil || job.Status != "completed" || !job.Result.Cached {
			t.Fatalf("cache submission failed: %+v %v", job, err)
		}
	}
	if p.submissions.Load() != 2 {
		t.Fatalf("cached submissions=%d", p.submissions.Load())
	}
}
