package sessionstore

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedReplacementAndLock(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "account.session")}
	first := []byte(`{"token":"synthetic-secret","cookies":[]}`)
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(disk, []byte("synthetic-secret")) {
		t.Fatal("plaintext secret on disk")
	}
	clear, err := store.Load()
	if err != nil || !bytes.Equal(clear, first) {
		t.Fatal("round trip failed", err)
	}
	if err = store.Save([]byte("replacement")); err != nil {
		t.Fatal("replace failed", err)
	}
	clear, err = store.Load()
	if err != nil || string(clear) != "replacement" {
		t.Fatal("replacement not durable", err)
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
	release, err = store.Lock()
	if err != nil {
		t.Fatal("lock not released", err)
	}
	release()
	if err = os.WriteFile(store.Path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Load(); err == nil {
		t.Fatal("corrupt data accepted")
	}
	if err = store.Delete(); err != nil {
		t.Fatal(err)
	}
}
