package storage

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

func TestCacheDisableAtomicAndHistoryPermanent(t *testing.T) {
	s := testDB(t)
	_ = s.SetSettings(map[string]string{"EVA_RESULT_CACHE_TTL": "30m"})
	const cache = ".state/eva-results/search.json"
	if e := s.WriteState(cache, []byte(`{"result":{"complete":true}}`)); e != nil {
		t.Fatal(e)
	}
	if e := s.WriteState(".state/eva/member.json", []byte(`{"session":"preserve"}`)); e != nil {
		t.Fatal(e)
	}
	if e := s.SaveJob("old", json.RawMessage(`{"id":"old","status":"completed"}`)); e != nil {
		t.Fatal(e)
	}
	if e := s.SetCachePolicy(false, false); !errors.Is(e, ErrCacheConfirmation) {
		t.Fatal("destructive toggle accepted without confirmation")
	}
	if _, e := s.ReadState(cache); e != nil {
		t.Fatal("unconfirmed disable deleted cache")
	}
	if e := s.SetCachePolicy(false, true); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ReadState(cache); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("cache survived disabling")
	}
	if e := s.WriteState(cache, []byte(`{"result":{"complete":true}}`)); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := s.DB.QueryRow("SELECT COUNT(*) FROM states WHERE key LIKE '.state/eva-results/%'").Scan(&count); e != nil || count != 0 {
		t.Fatal("in-flight writer repopulated disabled cache")
	}
	if _, e := s.ReadState(".state/eva/member.json"); e != nil {
		t.Fatal("login session deleted")
	}
	_, _ = s.DB.Exec("UPDATE jobs SET updated_at=?", time.Now().AddDate(-10, 0, 0).UnixMilli())
	_ = s.SetSettings(map[string]string{"HISTORY_RETENTION_DAYS": "1"})
	if e := s.Prune(); e != nil {
		t.Fatal(e)
	}
	if _, e := s.LoadJob("old"); e != nil {
		t.Fatal("old search history deleted")
	}
	if e := s.SetCachePolicy(true, false); e != nil {
		t.Fatal(e)
	}
	if e := s.WriteState(cache, []byte(`{"result":{"complete":true}}`)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ReadState(cache); e != nil {
		t.Fatal("re-enabled cache cannot save results")
	}
}
func TestConcurrentFinishingSearchesCannotRefillDisabledCache(t *testing.T) {
	s := testDB(t)
	_ = s.SetSettings(map[string]string{"EVA_RESULT_CACHE_TTL": "30m"})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 15; n++ {
				if e := s.WriteState(".state/eva-results/ongoing.json", []byte(`{"result":{"complete":true}}`)); e != nil {
					t.Error(e)
				}
			}
		}()
	}
	if e := s.SetCachePolicy(false, true); e != nil {
		t.Fatal(e)
	}
	wg.Wait()
	var count int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM states WHERE key LIKE '.state/eva-results/%'").Scan(&count)
	if count != 0 {
		t.Fatal("concurrent write escaped cache-disable policy")
	}
}
func TestLegacyZeroTTLAndGenericSettingsRequireConfirmation(t *testing.T) {
	s := testDB(t)
	e := s.ApplySettings(map[string]string{"EVA_RESULT_CACHE_TTL": "0ms"}, false)
	if !errors.Is(e, ErrCacheConfirmation) {
		t.Fatal("zero TTL bypassed warning")
	}
	if e = s.ApplySettings(map[string]string{"EVA_RESULT_CACHE_TTL": "0ms"}, true); e != nil {
		t.Fatal(e)
	}
	policy, _ := s.CachePolicy()
	if policy.Enabled {
		t.Fatal("zero TTL enabled")
	}
	if e = s.SetCachePolicy(true, false); e != nil {
		t.Fatal(e)
	}
	policy, _ = s.CachePolicy()
	if !policy.Enabled || policy.TTL != "30m" {
		t.Fatal("cache could not be enabled from legacy zero TTL")
	}
}
