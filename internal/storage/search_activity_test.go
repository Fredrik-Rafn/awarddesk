package storage

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestSearchActivityStartupGraceSurvivesRestart(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	if err := db.InitializeSearchActivity(now); err != nil {
		t.Fatal(err)
	}
	if err := db.InitializeSearchActivity(now.Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	last, err := db.SearchActivity()
	if err != nil || !last.Equal(now) {
		t.Fatalf("restart renewed startup grace: last=%s error=%v", last, err)
	}
}

func TestSearchActivityInitializesFromLatestSubmissionNotProgress(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	for i, age := range []time.Duration{8 * time.Hour, 2 * time.Hour} {
		raw, _ := json.Marshal(map[string]any{"id": fmt.Sprint(i), "createdAt": now.Add(-age), "updatedAt": now})
		if err := db.SaveJob(fmt.Sprint(i), raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.InitializeSearchActivity(now); err != nil {
		t.Fatal(err)
	}
	last, err := db.SearchActivity()
	if err != nil || !last.Equal(now.Add(-2*time.Hour)) {
		t.Fatalf("last=%s error=%v", last, err)
	}
}

func TestSearchActivityCannotMoveBackwardsUnderConcurrentSubmissions(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	if err := db.InitializeSearchActivity(now); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := db.RecordSearchActivity(now.Add(time.Duration(i) * time.Millisecond)); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	last, err := db.SearchActivity()
	if err != nil || !last.Equal(now.Add(31*time.Millisecond)) {
		t.Fatalf("last=%s error=%v", last, err)
	}
	if _, err := db.RecordSearchActivity(now); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.SearchActivity(); !got.Equal(last) {
		t.Fatal("older submission moved activity backwards")
	}
}

func TestSearchActivityFutureClockIsNormalizedAtStartup(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	if _, err := db.RecordSearchActivity(now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.InitializeSearchActivity(now); err != nil {
		t.Fatal(err)
	}
	if last, _ := db.SearchActivity(); !last.Equal(now) {
		t.Fatal("future activity extends restart grace")
	}
}
