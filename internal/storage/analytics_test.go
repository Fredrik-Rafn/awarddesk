package storage

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"awarddesk/internal/award"
)

func seedSearch(t *testing.T, s *Store, id, status string, cached bool, recoveries int, created time.Time, duration time.Duration) {
	t.Helper()
	events := []award.Recovery{}
	for i := 0; i < recoveries; i++ {
		events = append(events, award.Recovery{Code: "connection", Stage: "availability", Attempt: i + 1})
	}
	data := map[string]any{"id": id, "status": status, "query": award.Query{Provider: "eva", Origin: "TPE", Destination: "HKG", Date: "2026-11-30", Cabin: award.Business, Adults: 1}, "createdAt": created, "updatedAt": created.Add(duration), "recoveries": events, "progress": award.Progress{Stage: "completed"}, "result": map[string]any{"cached": cached, "itineraries": []map[string]string{{"id": "flight"}}, "privateUnneededPayload": "should-not-be-in-history"}}
	if status == "failed" {
		data["error"] = &award.Error{Code: "session_denied", Stage: "session preparation"}
	}
	b, _ := json.Marshal(data)
	if e := s.SaveJob(id, b); e != nil {
		t.Fatal(e)
	}
}
func TestAnalyticsSeparatesCacheRecoveryAndFailedOutcomes(t *testing.T) {
	s := testDB(t)
	now := time.Now().UTC()
	at := now.Add(-30 * time.Minute)
	seedSearch(t, s, "live-1", "completed", false, 2, at, 10*time.Second)
	seedSearch(t, s, "live-2", "completed", false, 0, at, 30*time.Second)
	seedSearch(t, s, "cached", "completed", true, 0, at, 4*time.Millisecond)
	seedSearch(t, s, "failed", "failed", false, 1, at, 20*time.Second)
	seedSearch(t, s, "cancelled", "cancelled", false, 0, at, 5*time.Second)
	seedSearch(t, s, "interrupted", "interrupted", false, 0, at, 5*time.Second)
	seedSearch(t, s, "running", "running", false, 0, at, 5*time.Second)
	seedSearch(t, s, "outside", "completed", false, 0, now.Add(-48*time.Hour), time.Second)
	a, e := s.SearchAnalytics(now.Add(-24*time.Hour), now)
	if e != nil {
		t.Fatal(e)
	}
	if a.Total != 7 || a.Completed != 3 || a.Failed != 1 || a.LiveCompleted != 2 || a.LiveFailed != 1 || a.CacheHits != 1 || a.Recovered != 1 || a.RetryEvents != 3 || a.Running != 1 {
		t.Fatalf("incorrect counters: %+v", a)
	}
	if a.SuccessRate == nil || *a.SuccessRate != 75 || a.LiveSuccessRate == nil || *a.LiveSuccessRate < 66 || *a.LiveSuccessRate > 67 {
		t.Fatal("wrong success-rate denominators")
	}
	if *a.MedianLiveMS != 20000 || *a.P95LiveMS != 30000 {
		t.Fatal("wrong duration percentiles")
	}
	if len(a.Failures) != 1 || a.Failures[0].Code != "session_denied" || len(a.Active) != 1 {
		t.Fatal("missing breakdowns")
	}
	total := 0
	for _, b := range a.Trend {
		total += b.Success + b.Failed + b.Cached + b.Other
	}
	if total != a.Total {
		t.Fatal("chart counts differ from summary")
	}
}
func TestHistoryPagesAllRecordsFiltersAndStripsPayload(t *testing.T) {
	s := testDB(t)
	now := time.Now().UTC()
	for i := 0; i < 127; i++ {
		status := "completed"
		if i%2 == 0 {
			status = "failed"
		}
		seedSearch(t, s, fmt.Sprintf("search-%03d", i), status, i%3 == 0, 1, now.Add(-time.Duration(i)*time.Minute), time.Second)
	}
	seen := map[string]bool{}
	for page := 1; page <= 6; page++ {
		v, e := s.SearchHistory(HistoryFilter{Page: page, Limit: 25})
		if e != nil {
			t.Fatal(e)
		}
		if v.Total != 127 {
			t.Fatal("history was truncated")
		}
		for _, r := range v.Items {
			if seen[r.ID] {
				t.Fatal("duplicate page item")
			}
			seen[r.ID] = true
			b, _ := json.Marshal(r)
			if strings.Contains(string(b), "should-not-be-in-history") {
				t.Fatal("payload leaked")
			}
			if r.Flights != 1 || r.Recoveries != 1 {
				t.Fatal("missing summary")
			}
		}
	}
	if len(seen) != 127 {
		t.Fatal("missing history")
	}
	v, e := s.SearchHistory(HistoryFilter{Text: "tpe hkg", Status: "failed", Mode: "cached", Limit: 100})
	if e != nil || v.Total != 22 {
		t.Fatalf("filter total=%d err=%v", v.Total, e)
	}
	v, e = s.SearchHistory(HistoryFilter{Text: "%' OR 1=1 --", Limit: 100})
	if e != nil || v.Total != 0 {
		t.Fatal("unsafe text filter")
	}
	if _, e = s.SearchHistory(HistoryFilter{Sort: "DROP TABLE jobs"}); e == nil {
		t.Fatal("invalid sort accepted")
	}
}
func TestEmptyAnalyticsAndConcurrentReaders(t *testing.T) {
	s := testDB(t)
	a, e := s.SearchAnalytics(time.Time{}, time.Now())
	if e != nil || a.SuccessRate != nil || a.MedianLiveMS != nil || len(a.Trend) != 24 {
		t.Fatal("empty data reported misleading metrics")
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.CachedFlights(); e != nil {
				t.Error(e)
			}
			if _, e := s.SearchHistory(HistoryFilter{}); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
}
