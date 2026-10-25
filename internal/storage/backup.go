package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
)

func Backup(ctx context.Context, source, destination string) error {
	info, err := os.Stat(source)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("source database does not exist or is not a regular file")
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("backup destination must be a new file")
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(destination)
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(destination)
		}
	}()
	db, err := sql.Open("sqlite", filepath.ToSlash(source)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", filepath.ToSlash(destination)); err != nil {
		return errors.New("could not create a consistent SQLite backup")
	}
	copyDB, err := sql.Open("sqlite", filepath.ToSlash(destination)+"?mode=ro")
	if err != nil {
		return err
	}
	var check string
	err = copyDB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check)
	closeErr := copyDB.Close()
	if err != nil || check != "ok" || closeErr != nil {
		return errors.New("backup integrity check failed")
	}
	complete = true
	return nil
}
