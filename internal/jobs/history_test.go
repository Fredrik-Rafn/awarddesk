package jobs

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"awarddesk/internal/storage"
)

func TestExpiredMemoryJobReadsPermanentHistoryImmediately(t *testing.T) {
	db, e := storage.Open(filepath.Join(t.TempDir(), "history.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := New()
	defer s.Close()
	if e = s.SetPersistence(db); e != nil {
		t.Fatal(e)
	}
	j := Job{ID: "old-history", Status: "completed", UpdatedAt: time.Now().Add(-48 * time.Hour)}
	b, _ := json.Marshal(j)
	db.SaveJob(j.ID, b)
	s.items[j.ID] = &j
	if got, ok := s.Get(j.ID); !ok || got.ID != j.ID {
		t.Fatal("memory eviction temporarily hid permanent history")
	}
}
