package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupIncludesCommittedWALAndLeavesSourceUsable(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source.db")
	s, err := Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.SetSettings(map[string]string{"HYPER_API_KEY": "synthetic-key"}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "backup.db")
	if err = Backup(context.Background(), source, destination); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", destination+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err = db.QueryRow("SELECT value FROM settings WHERE key='HYPER_API_KEY'").Scan(&value); err != nil || value != "synthetic-key" {
		t.Fatal("backup omitted committed WAL data")
	}
	if err = s.SetSettings(map[string]string{"SEARCH_CONCURRENCY": "5"}); err != nil {
		t.Fatal("backup interrupted the source database")
	}
	if err = Backup(context.Background(), source, destination); err == nil {
		t.Fatal("backup overwrote an existing file")
	}
}

func TestBackupRejectsMissingSourceAndCleansCancelledOutput(t *testing.T) {
	dir := t.TempDir()
	source, destination := filepath.Join(dir, "source.db"), filepath.Join(dir, "backup.db")
	if err := Backup(context.Background(), source, destination); err == nil {
		t.Fatal("missing source accepted")
	}
	s, err := Open(source)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = Backup(ctx, source, destination); err == nil {
		t.Fatal("cancelled backup succeeded")
	}
	if _, err = os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("cancelled backup left a partial file")
	}
}
