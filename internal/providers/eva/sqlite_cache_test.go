package eva

import (
	"awarddesk/internal/award"
	"awarddesk/internal/storage"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteCacheThirtyMinutesAndProviderRestart(t *testing.T) {
	db, e := storage.Open(filepath.Join(t.TempDir(), "cache.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	q := award.Query{Provider: "eva", Origin: "TPE", Destination: "HKG", Date: time.Now().AddDate(0, 0, 1).Format("2006-01-02"), Cabin: award.Business, Adults: 1}
	c := Config{Email: "test@example.test", Password: "test", StateBackend: db, ResultCacheTTL: 30 * time.Minute}
	p := New(c)
	fetched := time.Now().UTC().Add(-29 * time.Minute)
	r := award.Result{Query: q, Complete: true, FetchedAt: fetched}
	b, _ := json.Marshal(cachedResult{Version: 1, Result: r})
	if e = p.resultStore(q).Save(b); e != nil {
		t.Fatal(e)
	}
	p.Close()
	p = New(c)
	defer p.Close()
	hit, ok := p.CachedSearch(q)
	if !ok || !hit.Cached || hit.CacheExpiresAt == nil || hit.CacheExpiresAt.Sub(hit.FetchedAt) != 30*time.Minute {
		t.Fatal("30 minute result did not survive restart")
	}
	q.Refresh = true
	if _, ok = p.CachedSearch(q); ok {
		t.Fatal("live refresh used cached result")
	}
	q.Refresh = false
	r.FetchedAt = time.Now().Add(-31 * time.Minute)
	b, _ = json.Marshal(cachedResult{Version: 1, Result: r})
	if e = p.resultStore(q).Save(b); e != nil {
		t.Fatal(e)
	}
	if _, ok = p.CachedSearch(q); ok {
		t.Fatal("expired result reused")
	}
}
