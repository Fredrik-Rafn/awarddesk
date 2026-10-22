package fleet

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"awarddesk/internal/award"
)

func TestPrepareAllQueuesDeduplicatesAndHonorsSharedLimit(t *testing.T) {
	f, providers := setup(t, 40)
	rows, _ := f.db.Accounts()
	for _, a := range rows {
		switch a.ID {
		case "b":
			a.Enabled = false
			f.db.PutAccount(a)
		case "c":
			a.Password = ""
			f.db.PutAccount(a)
		}
	}
	f.db.Runtime("account:a", Status{ID: "a", State: "ready"})
	var active, peak atomic.Int32
	release := make(chan struct{})
	for id, p := range providers {
		if id == "a" {
			continue
		}
		p.info.Ready = false
		p.run = func(ctx context.Context) (award.Result, error) {
			n := active.Add(1)
			defer active.Add(-1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}
			select {
			case <-release:
				return award.Result{}, nil
			case <-ctx.Done():
				return award.Result{}, ctx.Err()
			}
		}
	}
	result, e := f.RequestPrepareAll()
	if e != nil || result.Queued != 37 || result.Skipped != 2 || result.Unconfigured != 1 {
		t.Fatal("incorrect batch selection", result, e)
	}
	again, e := f.RequestPrepareAll()
	if e != nil || again.Queued != 0 {
		t.Fatal("batch duplicated queued accounts")
	}
	deadline := time.Now().Add(4 * time.Second)
	for active.Load() < 4 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if active.Load() != 4 || peak.Load() > 4 {
		t.Fatal("worker limit not respected")
	}
	states, _ := f.Statuses()
	queued := 0
	for _, s := range states {
		if s.Queued {
			queued++
		}
	}
	if queued < 30 {
		t.Fatal("waiting accounts not reported as queued")
	}
	close(release)
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		pending := len(f.pending)
		f.mu.Unlock()
		if pending == 0 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	f.mu.Lock()
	left := len(f.pending)
	f.mu.Unlock()
	if left != 0 {
		t.Fatal("preparation queue did not drain")
	}
	for id, p := range providers {
		if id != "a" && id != "b" && id != "c" && p.calls.Load() != 1 {
			t.Fatal("account preparation missing or repeated")
		}
	}
}
