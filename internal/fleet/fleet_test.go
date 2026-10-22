package fleet

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/providers/eva"
	"awarddesk/internal/storage"
)

type fakeAirline struct {
	run     func(context.Context) (award.Result, error)
	info    eva.SessionInfo
	calls   atomic.Int32
	busy    atomic.Bool
	overlap atomic.Bool
}

func (p *fakeAirline) Search(ctx context.Context, q award.Query, r award.Reporter) (award.Result, error) {
	p.calls.Add(1)
	if p.busy.Swap(true) {
		p.overlap.Store(true)
	}
	defer p.busy.Store(false)
	return p.run(ctx)
}
func (p *fakeAirline) Ensure(ctx context.Context, r award.Reporter) error {
	_, e := p.Search(ctx, award.Query{}, r)
	return e
}
func (p *fakeAirline) CachedSearch(award.Query) (award.Result, bool) { return award.Result{}, false }
func (p *fakeAirline) SessionInfo() eva.SessionInfo                  { return p.info }
func (p *fakeAirline) Close()                                        {}
func setup(t *testing.T, n int) (*Fleet, map[string]*fakeAirline) {
	t.Helper()
	db, e := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	_ = db.SetSettings(map[string]string{"SEARCH_CONCURRENCY": "4", "EVA_RESULT_CACHE_TTL": "30m", "EVA_SESSION_MAX_AGE": "3h"})
	f := New(db)
	fakes := map[string]*fakeAirline{}
	for i := 0; i < n; i++ {
		id := string(rune('a' + i))
		a := storage.Account{ID: id, Email: id + "@example.com", Password: "test", OutlookEmail: id + "@outlook.com", ClientID: "client", RefreshToken: "token", Enabled: true}
		if e = db.PutAccount(a); e != nil {
			t.Fatal(e)
		}
		fakes[id] = &fakeAirline{info: eva.SessionInfo{Ready: true}, run: func(context.Context) (award.Result, error) { return award.Result{Complete: true}, nil }}
	}
	f.build = func(a storage.Account, m map[string]string) airline { return fakes[a.ID] }
	t.Cleanup(func() { f.Close(); db.Close() })
	return f, fakes
}
func TestIndependentAccountsConcurrentAndExclusive(t *testing.T) {
	f, p := setup(t, 3)
	var active, max atomic.Int32
	for _, v := range p {
		v.run = func(ctx context.Context) (award.Result, error) {
			n := active.Add(1)
			for {
				m := max.Load()
				if n <= m || max.CompareAndSwap(m, n) {
					break
				}
			}
			defer active.Add(-1)
			select {
			case <-time.After(30 * time.Millisecond):
				return award.Result{Complete: true}, nil
			case <-ctx.Done():
				return award.Result{}, ctx.Err()
			}
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 18; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, e := f.Search(ctx, award.Query{Refresh: true}, nil); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if max.Load() != 3 {
		t.Fatalf("concurrency=%d", max.Load())
	}
	for _, v := range p {
		if v.overlap.Load() {
			t.Fatal("account overlap")
		}
	}
}
func TestReadySessionPreferredAndSafeFailover(t *testing.T) {
	f, p := setup(t, 3)
	p["a"].info.Ready = false
	p["b"].run = func(context.Context) (award.Result, error) {
		return award.Result{}, &award.Error{Code: "session_expired", Stage: "session verification"}
	}
	var recovery int
	_, e := f.Search(context.Background(), award.Query{Refresh: true}, func(p award.Progress) {
		if p.Recovery != nil {
			recovery++
		}
	})
	if e != nil || p["c"].calls.Load() != 1 || p["a"].calls.Load() != 0 || recovery != 1 {
		t.Fatalf("ready failover failed: %v", e)
	}
}
func TestUnknownOutcomeAndPricingNeverFailOver(t *testing.T) {
	for _, err := range []*award.Error{{Code: "timeout", OutcomeUnknown: true}, {Code: "timeout", Stage: "miles pricing"}, {Code: "session_expired", Stage: "credentials"}} {
		t.Run(err.Stage+err.Code, func(t *testing.T) {
			f, p := setup(t, 2)
			p["a"].run = func(context.Context) (award.Result, error) { return award.Result{}, err }
			_, e := f.Search(context.Background(), award.Query{Refresh: true}, nil)
			if e == nil || p["b"].calls.Load() != 0 {
				t.Fatal("unsafe request was replayed")
			}
		})
	}
}
func TestBusyAccountCannotBeEditedAndWaitCancels(t *testing.T) {
	f, _ := setup(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, _, release, e := f.claim(ctx, "a", nil, false)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	if e = f.Edit("a", func() error { return nil }); e == nil {
		t.Fatal("edited busy account")
	}
	_, _, _, e = f.claim(ctx, "a", nil, false)
	if e == nil {
		t.Fatal("wait ignored cancellation")
	}
}
func TestFleetConcurrencySetting(t *testing.T) {
	f, _ := setup(t, 2)
	_ = f.db.SetSettings(map[string]string{"SEARCH_CONCURRENCY": "1"})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, _, release, e := f.claim(ctx, "a", nil, false)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	_, _, _, e = f.claim(ctx, "b", nil, false)
	if e == nil {
		t.Fatal("concurrency setting ignored")
	}
}

func TestMaintenancePreparesAccountsAndCoalescesManualRequests(t *testing.T) {
	f, p := setup(t, 3)
	_ = f.db.SetSettings(map[string]string{"HYPER_API_KEY": "test", "ANYSOLVER_API_KEY": "test", "PROXY_LIST": "test"})
	release := make(chan struct{})
	for _, v := range p {
		v.run = func(ctx context.Context) (award.Result, error) {
			select {
			case <-release:
				return award.Result{}, nil
			case <-ctx.Done():
				return award.Result{}, ctx.Err()
			}
		}
	}
	// Attention flags survive restarts and exclude only the affected account.
	_ = f.db.Runtime("account:a", Status{ID: "a", NeedsAttention: true})
	f.maintain()
	deadline := time.Now().Add(time.Second)
	for p["b"].calls.Load()+p["c"].calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p["a"].calls.Load() != 0 || p["b"].calls.Load()+p["c"].calls.Load() != 2 {
		t.Fatal("maintenance did not prepare independent eligible accounts")
	}
	if e := f.RequestPrepare("b"); e != nil {
		t.Fatal(e)
	}
	if p["b"].calls.Load() != 1 {
		t.Fatal("duplicate preparation")
	}
	close(release)
}

func TestCancelledSearchDoesNotQuarantineAccount(t *testing.T) {
	f, p := setup(t, 1)
	f.outcome("a", p["a"], context.Canceled)
	var s Status
	f.db.RuntimeValue("account:a", &s)
	if !s.NextAttempt.IsZero() || s.NeedsAttention {
		t.Fatal("cancelled job blocked healthy account")
	}
}

func TestClaimSkipsAccountLeasedByAnotherProcess(t *testing.T) {
	f, _ := setup(t, 2)
	release, e := f.db.LockState("account:a")
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	a, _, done, e := f.claim(ctx, "", nil, false)
	if e != nil {
		t.Fatal(e)
	}
	defer done()
	if a.ID != "b" {
		t.Fatal("did not skip external account lease")
	}
}

func TestProviderPanicReleasesAccountAndNeedsAttention(t *testing.T) {
	f, p := setup(t, 1)
	p["a"].run = func(context.Context) (award.Result, error) { panic("simulated provider panic") }
	_, e := f.Search(context.Background(), award.Query{Refresh: true}, nil)
	if e == nil {
		t.Fatal("panic was swallowed as success")
	}
	if f.entries["a"].busy {
		t.Fatal("panic leaked account ownership")
	}
	var status Status
	f.db.RuntimeValue("account:a", &status)
	if !status.NeedsAttention {
		t.Fatal("uncertain account automatically retried")
	}
	if e = f.Edit("a", func() error { return nil }); e != nil {
		t.Fatal("panic leaked account lease")
	}
}
