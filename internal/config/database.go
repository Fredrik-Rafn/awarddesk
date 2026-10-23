package config

import (
	"errors"
	"os"
	"path/filepath"

	"awarddesk/internal/storage"
)

func DatabasePath() string {
	path := os.Getenv("AWARDDESK_DB")
	if path == "" {
		path = filepath.Join("data", "awarddesk.db")
	}
	return path
}

func Database() (*storage.Store, error) {
	path := DatabasePath()
	db, e := storage.Open(path)
	if e != nil {
		return nil, errors.New("could not open workspace database")
	}
	if db.Setting("_IMPORTED") == "" {
		if e = LoadEnv(".env"); e == nil {
			e = db.ImportLegacy(".")
		}
		if e != nil {
			db.Close()
			return nil, errors.New("could not import existing workspace files")
		}
	}
	return db, nil
}
