package jobs

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"awarddesk/internal/award"
)

type blockedHistory struct {
	entered, release chan struct{}
	once             sync.Once
	mu               sync.Mutex
	data             map[string][]byte
}

func (p *blockedHistory) SaveJob(id string, data []byte) error {
	p.once.Do(func() { close(p.entered) })
	<-p.release
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data[id] = append([]byte(nil), data...)
	return nil
}
func (p *blockedHistory) Jobs() ([]json.RawMessage, error) { return nil, nil }
func (p *blockedHistory) LoadJob(id string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.data[id]...), nil
}

func TestSlowHistoryDoesNotBlockStatusOrCancelAndFinalStateFlushes(t *testing.T) {
	store := &blockedHistory{entered: make(chan struct{}), release: make(chan struct{}), data: make(map[string][]byte)}
	s := New(fakeProvider{func(ctx context.Context) (award.Result, error) { <-ctx.Done(); return award.Result{}, ctx.Err() }})
	if err := s.SetPersistence(store); err != nil {
		t.Fatal(err)
	}
	submitted := make(chan Job, 1)
	go func() { j, _ := s.Submit(query()); submitted <- j }()
	<-store.entered
	items := s.List()
	if len(items) != 1 {
		close(store.release)
		s.Close()
		t.Fatal("pending admission missing")
	}
	j := items[0]
	done := make(chan struct{})
	go func() { s.Get(j.ID); s.Cancel(j.ID); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(store.release)
		s.Close()
		t.Fatal("SQL write blocked scheduler")
	}
	close(store.release)
	<-submitted
	s.Close()
	data, _ := store.LoadJob(j.ID)
	var saved Job
	if json.Unmarshal(data, &saved) != nil || saved.Status != "cancelled" || saved.FinishedAt == nil {
		t.Fatal("terminal state lost or overwritten by old progress")
	}
}

type slowCache struct {
	fakeProvider
	entered, release chan struct{}
}

func (p slowCache) CachedSearch(q award.Query) (award.Result, bool) {
	if q.Destination != "SFO" {
		return award.Result{}, false
	}
	close(p.entered)
	<-p.release
	return award.Result{Query: q, Complete: true, Cached: true, FetchedAt: time.Now()}, true
}
func TestSlowCacheDoesNotBlockUnrelatedCancellation(t *testing.T) {
	p := slowCache{fakeProvider: fakeProvider{func(ctx context.Context) (award.Result, error) { <-ctx.Done(); return award.Result{}, ctx.Err() }}, entered: make(chan struct{}), release: make(chan struct{})}
	s := New(p)
	defer s.Close()
	j, err := s.Submit(query())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { q := query(); q.Destination = "SFO"; _, err := s.Submit(q); done <- err }()
	<-p.entered
	cancelled := make(chan bool, 1)
	go func() { cancelled <- s.Cancel(j.ID) }()
	select {
	case ok := <-cancelled:
		if !ok {
			t.Fatal("cancel failed")
		}
	case <-time.After(time.Second):
		close(p.release)
		t.Fatal("cache I/O blocked scheduler")
	}
	close(p.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
