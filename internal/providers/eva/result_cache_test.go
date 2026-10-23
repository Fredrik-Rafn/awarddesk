package eva

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"awarddesk/internal/award"
)

func cacheQuery() award.Query {
	return award.Query{Provider: "eva", Origin: "HND", Destination: "SIN", Date: time.Now().AddDate(0, 0, 1).Format("2006-01-02"), Cabin: award.Economy, Adults: 1}
}

func TestTaxEnrichmentCacheUpgradePreservesCashButRefreshesOldAwards(t *testing.T) {
	t.Chdir(t.TempDir())
	p := New(Config{Email: "member@example.test", ResultCacheTTL: time.Minute})
	for _, mode := range []string{"", "award"} {
		q := cacheQuery()
		q.Mode = mode
		data, _ := json.Marshal(cachedResult{Version: 2, Result: award.Result{Query: q, FetchedAt: time.Now(), Complete: true}})
		if err := p.resultStore(q).Save(data); err != nil {
			t.Fatal(err)
		}
		_, hit := p.CachedSearch(q)
		if hit != (mode == "") {
			t.Fatalf("legacy cache mode=%q accepted=%v", mode, hit)
		}
	}
}

func TestLegacySingleAdultCacheSurvivesPartyTotalMigration(t *testing.T) {
	t.Chdir(t.TempDir())
	p := New(Config{Email: "member@example.test", ResultCacheTTL: time.Minute})
	for _, adults := range []int{1, 2} {
		q := cacheQuery()
		q.Adults = adults
		data, _ := json.Marshal(cachedResult{Version: 1, Result: award.Result{Query: q, FetchedAt: time.Now(), Complete: true}})
		if err := p.resultStore(q).Save(data); err != nil {
			t.Fatal(err)
		}
		_, hit := p.CachedSearch(q)
		if hit != (adults == 1) {
			t.Fatalf("legacy cache adults=%d accepted=%v", adults, hit)
		}
	}
}

func TestResultCachePersistenceRefreshAndIsolation(t *testing.T) {
	t.Chdir(t.TempDir())
	p := New(Config{Email: "one@example.test", ResultCacheTTL: 2 * time.Minute})
	q := cacheQuery()
	calls := 0
	run := func(_ context.Context, q award.Query, _ award.Reporter) (award.Result, error) {
		calls++
		if q.Refresh {
			t.Fatal("refresh control leaked into canonical query")
		}
		return award.Result{Query: q, FetchedAt: time.Now(), Complete: true, Itineraries: []award.Itinerary{{ID: "test", Fares: []award.Fare{{Name: "Eligible fare"}}}}}, nil
	}
	first, err := p.searchCached(context.Background(), q, nil, run)
	if err != nil || first.Cached || calls != 1 {
		t.Fatalf("first result: calls=%d cached=%v err=%v", calls, first.Cached, err)
	}

	p = New(p.config)
	second, err := p.searchCached(context.Background(), q, nil, run)
	if err != nil || !second.Cached || calls != 1 || !second.FetchedAt.Equal(first.FetchedAt) {
		t.Fatalf("cache hit: calls=%d result=%+v err=%v", calls, second, err)
	}
	second.Itineraries[0].ID = "changed"
	again, _ := p.CachedSearch(q)
	if again.Itineraries[0].ID != "test" {
		t.Fatal("returned result mutated cache")
	}
	q.Refresh = true
	_, err = p.searchCached(context.Background(), q, nil, run)
	if err != nil || calls != 2 {
		t.Fatal("forced refresh did not run live")
	}
	q.Refresh = false
	changed := q
	changed.Cabin = award.Business
	if _, hit := p.CachedSearch(changed); hit {
		t.Fatal("cache crossed cabins")
	}
	other := New(Config{Email: "two@example.test", ResultCacheTTL: 2 * time.Minute})
	if _, hit := other.CachedSearch(q); hit {
		t.Fatal("cache crossed accounts")
	}
}

func TestResultCacheRejectsStalePartialCorruptAndFailedResults(t *testing.T) {
	t.Chdir(t.TempDir())
	p := New(Config{Email: "one@example.test", ResultCacheTTL: time.Minute})
	q := cacheQuery()
	for _, tc := range []struct {
		name    string
		result  award.Result
		version int
	}{
		{"stale", award.Result{Query: q, FetchedAt: time.Now().Add(-2 * time.Minute), Complete: true}, resultCacheVersion},
		{"future", award.Result{Query: q, FetchedAt: time.Now().Add(time.Minute), Complete: true}, resultCacheVersion},
		{"partial", award.Result{Query: q, FetchedAt: time.Now()}, resultCacheVersion},
		{"old format", award.Result{Query: q, FetchedAt: time.Now(), Complete: true}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, _ := json.Marshal(cachedResult{Version: tc.version, Result: tc.result})
			if err := p.resultStore(q).Save(data); err != nil {
				t.Fatal(err)
			}
			if _, hit := p.CachedSearch(q); hit {
				t.Fatal("invalid cache entry reused")
			}
		})
	}
	if err := os.WriteFile(p.resultStore(q).Path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, hit := p.CachedSearch(q); hit {
		t.Fatal("corrupt entry reused")
	}
	_ = p.resultStore(q).Delete()
	want := errors.New("upstream failed")
	_, err := p.searchCached(context.Background(), q, nil, func(context.Context, award.Query, award.Reporter) (award.Result, error) { return award.Result{}, want })
	if err != want {
		t.Fatal(err)
	}
	if _, err = os.Stat(p.resultStore(q).Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failure cached")
	}
	p.config.ResultCacheTTL = 0
	_, _ = p.searchCached(context.Background(), q, nil, func(context.Context, award.Query, award.Reporter) (award.Result, error) {
		return award.Result{Query: q, FetchedAt: time.Now(), Complete: true}, nil
	})
	if _, hit := p.CachedSearch(q); hit {
		t.Fatal("disabled cache reused")
	}
}
