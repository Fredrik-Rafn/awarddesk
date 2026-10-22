package jobs

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/storage"
)

type loadProvider struct {
	entered, release chan struct{}
	active, peak     atomic.Int32
}

func (p *loadProvider) Capabilities() award.Capabilities { return fakeProvider{}.Capabilities() }
func (p *loadProvider) Search(ctx context.Context, q award.Query, report award.Reporter) (award.Result, error) {
	n := p.active.Add(1)
	defer p.active.Add(-1)
	for {
		old := p.peak.Load()
		if old >= n || p.peak.CompareAndSwap(old, n) {
			break
		}
	}
	p.entered <- struct{}{}
	select {
	case <-p.release:
	case <-ctx.Done():
		return award.Result{}, ctx.Err()
	}
	r := award.Result{Query: q, Complete: true, FetchedAt: time.Now().UTC(), Itineraries: []award.Itinerary{{ID: "flight", Fares: []award.Fare{{MilesCash: &award.MilesCash{Miles: 1000}}}}}}
	for step := 1; step <= 3; step++ {
		report(award.Progress{Stage: "miles pricing", Completed: step, Total: 3, Preview: &r})
	}
	return r, nil
}

func TestOneHundredWorkersWithSQLiteAndThreeHundredQueuedSearches(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "load.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p := &loadProvider{entered: make(chan struct{}, 300), release: make(chan struct{})}
	s := NewConcurrent(100, p)
	defer s.Close()
	if err = s.SetPersistence(db); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	ids := make([]string, 300)
	for i := range ids {
		j, e := s.Submit(queueQuery(i))
		if e != nil {
			t.Fatal(e)
		}
		ids[i] = j.ID
	}
	for i := 0; i < 100; i++ {
		select {
		case <-p.entered:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d workers started", i)
		}
	}
	for _, id := range ids[270:] {
		if !s.Cancel(id) {
			t.Fatal("queued cancellation failed")
		}
	}
	for i := 0; i < 20; i++ {
		j, e := s.Submit(queueQuery(i))
		if e != nil || j.ID != ids[i] {
			t.Fatal("duplicate work admitted")
		}
	}
	close(p.release)
	for _, id := range ids[:270] {
		await(t, s, id, "completed")
	}
	s.Close()
	rows, err := db.Jobs()
	if err != nil || len(rows) != 300 {
		t.Fatalf("history rows=%d error=%v", len(rows), err)
	}
	completed, cancelled := 0, 0
	for _, data := range rows {
		var j Job
		if json.Unmarshal(data, &j) != nil {
			t.Fatal("invalid persisted job")
		}
		switch j.Status {
		case "completed":
			completed++
		case "cancelled":
			cancelled++
		default:
			t.Fatal("unexpected terminal state", j.Status)
		}
	}
	if completed != 270 || cancelled != 30 || p.peak.Load() != 100 {
		t.Fatalf("completed=%d cancelled=%d peak=%d", completed, cancelled, p.peak.Load())
	}
	t.Logf("Synthetic local load: 300 jobs, peak %d, completed %d, queued cancellations %d, elapsed %s", p.peak.Load(), completed, cancelled, time.Since(started).Round(time.Millisecond))
}
