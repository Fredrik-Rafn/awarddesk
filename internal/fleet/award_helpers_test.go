package fleet

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/providers/eva"
	"awarddesk/internal/storage"
)

type helperAirline struct {
	*fakeAirline
	quote      func(context.Context, award.Query, []eva.AwardQuoteTarget, award.Reporter) (award.Result, error)
	quoteCalls atomic.Int32
}

func (p *helperAirline) SearchAwardQuotes(ctx context.Context, q award.Query, targets []eva.AwardQuoteTarget, report award.Reporter) (award.Result, error) {
	p.quoteCalls.Add(1)
	return p.quote(ctx, q, targets, report)
}

func helperFleet(t *testing.T, n int) (*Fleet, map[string]*helperAirline) {
	t.Helper()
	f, originals := setup(t, n)
	providers := map[string]*helperAirline{}
	for id, original := range originals {
		providers[id] = &helperAirline{fakeAirline: original, quote: func(ctx context.Context, q award.Query, targets []eva.AwardQuoteTarget, report award.Reporter) (award.Result, error) {
			return award.Result{Query: q, Complete: true}, nil
		}}
	}
	f.build = func(a storage.Account, _ map[string]string) airline { return providers[a.ID] }
	return f, providers
}

func TestAwardHelpersOnlyReserveReadyIdleAccountsWithinSharedCapacity(t *testing.T) {
	f, p := helperFleet(t, 6)
	if err := f.db.SetSettings(map[string]string{"SEARCH_CONCURRENCY": "3"}); err != nil {
		t.Fatal(err)
	}
	_, _, release, err := f.claim(context.Background(), "a", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	p["c"].info.Ready = false
	_ = f.db.Runtime("account:d", Status{ID: "d", NextAttempt: time.Now().Add(time.Minute)})
	_ = f.db.Runtime("account:e", Status{ID: "e", NeedsAttention: true})
	f.mu.Lock()
	f.pending["f"] = true
	f.registrations = 1
	f.mu.Unlock()
	factory, cleanup := f.awardHelperFactory(context.Background(), map[string]bool{"a": true})
	workers := factory(10)
	if len(workers) != 1 {
		t.Fatalf("workers=%d expected only ready b under shared cap", len(workers))
	}
	if err := f.Edit("b", func() error { return nil }); err == nil {
		t.Fatal("reserved helper account could be edited")
	}
	if len(factory(2)) != 0 {
		t.Fatal("factory reserved a second helper batch")
	}
	cleanup()
	if err := f.Edit("b", func() error { return nil }); err != nil {
		t.Fatal("unused helper lease was not released", err)
	}
	for id, provider := range p {
		if provider.calls.Load() != 0 || provider.quoteCalls.Load() != 0 {
			t.Fatalf("reserving %s invoked airline requests", id)
		}
	}
}

func TestAwardHelpersRunDistinctAccountsAndReleaseOnCancellation(t *testing.T) {
	f, p := helperFleet(t, 3)
	_ = f.db.SetSettings(map[string]string{"SEARCH_CONCURRENCY": "3"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, release, err := f.claim(ctx, "a", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	entered := make(chan string, 2)
	for id, provider := range p {
		provider.quote = func(ctx context.Context, q award.Query, targets []eva.AwardQuoteTarget, report award.Reporter) (award.Result, error) {
			if q.Mode != "award" || len(targets) != 1 || len(targets[0].Segments) != 1 {
				t.Error("helper received invalid targets")
			}
			entered <- id
			<-ctx.Done()
			return award.Result{Query: q}, ctx.Err()
		}
	}
	factory, cleanup := f.awardHelperFactory(ctx, map[string]bool{"a": true})
	workers := factory(2)
	if len(workers) != 2 {
		t.Fatalf("workers=%d", len(workers))
	}
	var wg sync.WaitGroup
	for i, worker := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := worker(ctx, award.Query{Mode: "award"}, []eva.AwardQuoteTarget{{Segments: []award.Segment{{FlightNumber: string(rune('1' + i))}}}}, nil)
			if !errors.Is(err, context.Canceled) {
				t.Error("cancellation lost", err)
			}
		}()
	}
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case id := <-entered:
			seen[id] = true
		case <-time.After(time.Second):
			t.Fatal("helpers did not run concurrently")
		}
	}
	if !seen["b"] || !seen["c"] {
		t.Fatal("helpers did not use distinct accounts", seen)
	}
	f.mu.Lock()
	busy := 0
	for _, entry := range f.entries {
		if entry.busy {
			busy++
		}
	}
	f.mu.Unlock()
	if busy != 3 {
		t.Fatalf("busy=%d expected parent and two helpers", busy)
	}
	cleanup()
	wg.Wait()
	if ctx.Err() != nil {
		t.Fatal("helper cleanup cancelled the parent search context")
	}
	for _, id := range []string{"b", "c"} {
		if err := f.Edit(id, func() error { return nil }); err != nil {
			t.Fatal("helper lease survived cancellation", err)
		}
		if p[id].calls.Load() != 0 || p[id].quoteCalls.Load() != 1 {
			t.Fatal("helper used ordinary search or repeated quotation")
		}
	}
	if _, err := workers[0](context.Background(), award.Query{}, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("closed helper could run again")
	}
}

func TestAwardHelperFactoryNeverWaitsForNestedCapacity(t *testing.T) {
	f, _ := helperFleet(t, 4)
	_, _, releaseA, err := f.claim(context.Background(), "a", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseA()
	_, _, releaseB, err := f.claim(context.Background(), "b", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseB()
	first, closeFirst := f.awardHelperFactory(context.Background(), map[string]bool{"a": true})
	defer closeFirst()
	if len(first(2)) != 2 {
		t.Fatal("spare shared capacity was not used")
	}
	second, closeSecond := f.awardHelperFactory(context.Background(), map[string]bool{"b": true})
	defer closeSecond()
	if len(second(2)) != 0 {
		t.Fatal("helper exceeded shared concurrency limit")
	}
}

func TestAwardHelperFailureOutcomeAndLeaseCleanup(t *testing.T) {
	for _, kind := range []string{"not-ready", "protocol", "denied", "panic"} {
		t.Run(kind, func(t *testing.T) {
			f, p := helperFleet(t, 2)
			p["b"].quote = func(ctx context.Context, q award.Query, targets []eva.AwardQuoteTarget, report award.Reporter) (award.Result, error) {
				switch kind {
				case "not-ready":
					return award.Result{}, &award.Error{Code: "award_helper_not_ready"}
				case "protocol":
					return award.Result{}, &award.Error{Code: "award_protocol", Stage: "award pricing"}
				case "denied":
					return award.Result{}, &award.Error{Code: "session_denied", Stage: "award pricing", HTTPStatus: 403}
				default:
					panic("synthetic")
				}
			}
			factory, cleanup := f.awardHelperFactory(context.Background(), map[string]bool{"a": true})
			defer cleanup()
			workers := factory(1)
			if len(workers) != 1 {
				t.Fatal("missing helper")
			}
			_, err := workers[0](context.Background(), award.Query{Mode: "award"}, nil, nil)
			if err == nil {
				t.Fatal("helper failure disappeared")
			}
			var status Status
			f.db.RuntimeValue("account:b", &status)
			if (kind == "not-ready" || kind == "protocol") && status.ConsecutiveFailures != 0 {
				t.Fatal("healthy account penalized", status)
			}
			if (kind == "denied" || kind == "panic") && status.ConsecutiveFailures != 1 {
				t.Fatal("failed session not recorded", status)
			}
			if err := f.Edit("b", func() error { return nil }); err != nil {
				t.Fatal("failed helper lease was not released", err)
			}
		})
	}
}
