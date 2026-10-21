package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testDB(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestConcurrentStatesAndCrossProcessLease(t *testing.T) {
	s := testDB(t)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			k := ID()
			if e := s.WriteState(k, []byte(`{"valid":true}`)); e != nil {
				t.Error(e)
			}
			if _, e := s.ReadState(k); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	rows, e := s.States()
	if e != nil || len(rows) != 32 {
		t.Fatalf("states=%d error=%v", len(rows), e)
	}
	release, e := s.LockState(`.state\eva\account.json`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.LockState(".state/eva/account.json"); e == nil {
		t.Fatal("duplicate account lease")
	}
	release()
	release()
	release, e = s.LockState(".state/eva/account.json")
	if e != nil {
		t.Fatal(e)
	}
	release()
}
func TestMigrationOnceAndReopen(t *testing.T) {
	root := t.TempDir()
	s, e := Open(filepath.Join(root, "data", "workspace.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("EVA_EMAIL", "test@example.com")
	t.Setenv("EVA_PASSWORD", "test-only")
	if e = os.MkdirAll(filepath.Join(root, ".state", "eva"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, ".state", "eva", "test.json"), []byte(`{"Version":1}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e = s.ImportLegacy(root); e != nil {
		t.Fatal(e)
	}
	if s.Setting("EVA_RESULT_CACHE_TTL") != "30m" {
		t.Fatal("cache default")
	}
	a, _ := s.Accounts()
	if len(a) != 1 {
		t.Fatal("account import")
	}
	if _, e = s.ReadState(".state/eva/test.json"); e != nil {
		t.Fatal(e)
	}
	if e = s.SetSettings(map[string]string{"HYPER_API_KEY": "changed"}); e != nil {
		t.Fatal(e)
	}
	t.Setenv("HYPER_API_KEY", "stale")
	if e = s.ImportLegacy(root); e != nil || s.Setting("HYPER_API_KEY") != "changed" {
		t.Fatal("legacy data overwrote admin changes")
	}
	s.Close()
	s, e = Open(filepath.Join(root, "data", "workspace.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.Setting("HYPER_API_KEY") != "changed" {
		t.Fatal("settings not durable")
	}
}
func TestPrune(t *testing.T) {
	s := testDB(t)
	_ = s.SetSettings(map[string]string{"HISTORY_RETENTION_DAYS": "1"})
	if e := s.SaveJob("old", json.RawMessage(`{"id":"old"}`)); e != nil {
		t.Fatal(e)
	}
	_, e := s.DB.Exec("UPDATE jobs SET updated_at=?", time.Now().Add(-48*time.Hour).UnixMilli())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Prune(); e != nil {
		t.Fatal(e)
	}
	j, _ := s.Jobs()
	if len(j) != 0 {
		t.Fatal("history not pruned")
	}
}
