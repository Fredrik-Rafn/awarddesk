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

func TestWorkersPersistResultsAndCoalesce(t *testing.T) {
	db, e := storage.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var active, peak, calls atomic.Int32
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	s := NewConcurrent(4, fakeProvider{func(ctx context.Context) (award.Result, error) {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for {
			m := peak.Load()
			if m >= n || peak.CompareAndSwap(m, n) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
			return award.Result{Complete: true}, nil
		case <-ctx.Done():
			return award.Result{}, ctx.Err()
		}
	}})
	if e = s.SetPersistence(db); e != nil {
		t.Fatal(e)
	}
	ids := []string{}
	for i := 0; i < 4; i++ {
		q := query()
		q.Date = time.Now().AddDate(0, 0, i+1).Format("2006-01-02")
		j, e := s.Submit(q)
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, j.ID)
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("worker did not start")
		}
	}
	duplicate, e := s.Submit(query())
	if e != nil || duplicate.ID != ids[0] {
		t.Fatal("identical search did not coalesce")
	}
	close(release)
	for _, id := range ids {
		await(t, s, id, "completed")
	}
	if peak.Load() != 4 || calls.Load() != 4 {
		t.Fatal("workers did not overlap or duplicate request ran")
	}
	s.Close()
	s = New()
	defer s.Close()
	if e = s.SetPersistence(db); e != nil {
		t.Fatal(e)
	}
	j, ok := s.Get(ids[0])
	if !ok || j.Status != "completed" {
		t.Fatal("completed result did not survive service restart")
	}
}
func TestRestartMarksInterruptedInsteadOfReplaying(t *testing.T) {
	db, e := storage.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	j := Job{ID: "interrupted", Query: query(), Status: "running", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	b, _ := json.Marshal(j)
	if e = db.SaveJob(j.ID, b); e != nil {
		t.Fatal(e)
	}
	s := New()
	defer s.Close()
	if e = s.SetPersistence(db); e != nil {
		t.Fatal(e)
	}
	j, ok := s.Get(j.ID)
	if !ok || j.Status != "interrupted" || j.Error == nil || !j.Error.OutcomeUnknown {
		t.Fatal("running job was replayed or status lost")
	}
}
