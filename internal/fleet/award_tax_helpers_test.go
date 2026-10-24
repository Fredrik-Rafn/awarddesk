package fleet

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/storage"
)

type taxHelperAirline struct {
	*helperAirline
	tax    func(context.Context, award.Result, award.Reporter) (award.Result, error)
	called int
}

func (p *taxHelperAirline) SearchAwardTaxEstimates(ctx context.Context, r award.Result, report award.Reporter) (award.Result, error) {
	p.called++
	return p.tax(ctx, r, report)
}

func taxHelperFleet(t *testing.T, count int) (*Fleet, map[string]*taxHelperAirline) {
	t.Helper()
	f, originals := helperFleet(t, count)
	providers := map[string]*taxHelperAirline{}
	for id, p := range originals {
		providers[id] = &taxHelperAirline{helperAirline: p, tax: func(_ context.Context, r award.Result, _ award.Reporter) (award.Result, error) { return r, nil }}
	}
	f.build = func(a storage.Account, _ map[string]string) airline { return providers[a.ID] }
	return f, providers
}

func TestAwardTaxHelperUsesIdleIndependentAccountAndReleasesLease(t *testing.T) {
	f, providers := taxHelperFleet(t, 3)
	_, _, parentRelease, err := f.claim(context.Background(), "a", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer parentRelease()
	providers["b"].info.Ready = false
	r := award.Result{Query: award.Query{Provider: "eva", Mode: "award", Origin: "SIN", Destination: "TPE", Adults: 2}, Complete: true, Itineraries: []award.Itinerary{{Fares: []award.Fare{{Points: 10000}}}}}
	r = award.CloneResult(r)
	before := award.CloneResult(r)
	providers["c"].tax = func(ctx context.Context, snapshot award.Result, report award.Reporter) (award.Result, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 90*time.Second {
			t.Fatal("unbounded tax helper")
		}
		f.mu.Lock()
		busy := f.entries["c"].busy && f.entries["a"].busy
		f.mu.Unlock()
		if !busy {
			t.Fatal("account was not exclusively leased")
		}
		snapshot.Itineraries[0].Fares[0].Points = 999
		return snapshot, errors.New("optional failure")
	}
	_, err = f.awardTaxWorker(map[string]bool{"a": true})(context.Background(), r, nil)
	if err == nil || providers["c"].called != 1 || providers["a"].called != 0 || providers["b"].called != 0 || !reflect.DeepEqual(r, before) {
		t.Fatal("tax fallback used wrong account, lost error, or changed parent result")
	}
	f.mu.Lock()
	busy := f.entries["c"].busy
	f.mu.Unlock()
	if busy || providers["c"].calls.Load() != 0 || providers["c"].quoteCalls.Load() != 0 {
		t.Fatal("helper lease leaked or performed award/ordinary search")
	}
}

func TestAwardTaxHelperHonorsSharedCapacityAndRemainingBudget(t *testing.T) {
	f, providers := taxHelperFleet(t, 2)
	if err := f.db.SetSettings(map[string]string{"SEARCH_CONCURRENCY": "1"}); err != nil {
		t.Fatal(err)
	}
	_, _, release, err := f.claim(context.Background(), "a", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	r := award.Result{Complete: true}
	f.awardTaxWorker(map[string]bool{"a": true})(context.Background(), r, nil)
	if providers["b"].called != 0 {
		t.Fatal("tax helper exceeded shared concurrency")
	}
	if err := f.db.SetSettings(map[string]string{"SEARCH_CONCURRENCY": "2"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
	defer cancel()
	f.awardTaxWorker(map[string]bool{"a": true})(ctx, r, nil)
	if providers["b"].called != 0 {
		t.Fatal("tax helper ran without useful remaining budget")
	}
}

func TestAwardTaxHelperCancellationAndPanicReleaseAccount(t *testing.T) {
	for _, panicWorker := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "panic"}[panicWorker], func(t *testing.T) {
			f, providers := taxHelperFleet(t, 2)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			providers["b"].tax = func(ctx context.Context, r award.Result, _ award.Reporter) (award.Result, error) {
				if panicWorker {
					panic("test")
				}
				cancel()
				<-ctx.Done()
				return r, ctx.Err()
			}
			_, err := f.awardTaxWorker(map[string]bool{"a": true})(ctx, award.Result{Complete: true}, nil)
			if err == nil {
				t.Fatal("helper error disappeared")
			}
			f.mu.Lock()
			busy := f.entries["b"].busy
			f.mu.Unlock()
			if busy {
				t.Fatal("helper account not released")
			}
		})
	}
}

func TestAwardTaxHelperWithoutTaxCapabilityDoesNotSearch(t *testing.T) {
	f, providers := helperFleet(t, 2)
	f.awardTaxWorker(map[string]bool{"a": true})(context.Background(), award.Result{}, nil)
	if providers["b"].calls.Load() != 0 || providers["b"].quoteCalls.Load() != 0 {
		t.Fatal("unsupported provider was searched")
	}
	f.mu.Lock()
	busy := f.entries["b"].busy
	f.mu.Unlock()
	if busy {
		t.Fatal("unsupported provider lease leaked")
	}
}

var _ awardTaxProvider = (*taxHelperAirline)(nil)
var _ awardQuoteProvider = (*taxHelperAirline)(nil)
