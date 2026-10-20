package sessionstore

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPortableJSONReplacementAndLock(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "account.json")}
	first := []byte(`{"token":"synthetic-secret","cookies":[]}`)
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(store.Path)
	if err != nil || !json.Valid(disk) || !bytes.Contains(disk, []byte("synthetic-secret")) || !bytes.Contains(disk, []byte("\n  ")) {
		t.Fatal("state is not readable, formatted JSON", err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(store.Path)
		if info.Mode().Perm() != 0600 {
			t.Fatal("state file must be owner-only")
		}
	}
	if err = store.Save([]byte(`{"token":"replacement"}`)); err != nil {
		t.Fatal(err)
	}
	data, err := store.Load()
	if err != nil || !bytes.Contains(data, []byte("replacement")) {
		t.Fatal("replacement not durable", err)
	}
	if err = store.Save([]byte("invalid")); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	data, err = store.Load()
	if err != nil || !bytes.Contains(data, []byte("replacement")) {
		t.Fatal("failed write damaged saved state")
	}
	release, err := store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if unlock, err := store.Lock(); err == nil {
		unlock()
		t.Fatal("concurrent lock accepted")
	}
	release()
	release()
	release, err = store.Lock()
	if err != nil {
		t.Fatal("lock not released", err)
	}
	release()
	if err = os.WriteFile(store.Path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Load(); err == nil {
		t.Fatal("corrupt state accepted")
	}
	if err = store.Delete(); err != nil {
		t.Fatal(err)
	}
}

func TestIndependentAccountFilesAndSizeLimits(t *testing.T) {
	dir := t.TempDir()
	a, b := Store{Path: filepath.Join(dir, "one.json")}, Store{Path: filepath.Join(dir, "two.json")}
	for i, store := range []Store{a, b} {
		data, _ := json.Marshal(map[string]int{"account": i})
		if err := store.Save(data); err != nil {
			t.Fatal(err)
		}
	}
	unlockA, err := a.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlockA()
	unlockB, err := b.Lock()
	if err != nil {
		t.Fatal("independent account blocked", err)
	}
	unlockB()
	if err = a.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Load(); err != nil {
		t.Fatal("deleting one account affected another", err)
	}
	if err = a.Save(bytes.Repeat([]byte(" "), maxSize+1)); err == nil {
		t.Fatal("oversized write accepted")
	}
	if err = os.WriteFile(a.Path, bytes.Repeat([]byte(" "), maxSize+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Load(); err == nil {
		t.Fatal("oversized file accepted")
	}
}
