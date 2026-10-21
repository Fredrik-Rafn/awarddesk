package config

import (
	"awarddesk/internal/antibot"
	"awarddesk/internal/providers/ana"
	"awarddesk/internal/proxies"
	"awarddesk/internal/storage"
	"errors"
	"os"
	"path/filepath"
)

// Database is shared by server and diagnostics; .env is only a first-run import.
func Database() (*storage.Store, error) {
	path := os.Getenv("AWARDDESK_DB")
	if path == "" {
		path = filepath.Join("data", "awarddesk.db")
	}
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
func ANAFromDatabase(db *storage.Store) (*ana.Provider, bool, error) {
	m, e := db.Settings()
	if e != nil {
		return nil, false, e
	}
	c := ana.Config{MemberNumber: m["ANA_MEMBER_NUMBER"], Password: m["ANA_PASSWORD"], Proxy: m["AWARDDESK_PROXY"], Challenge: antibot.Hyper{Key: m["HYPER_API_KEY"], VerifyAtLogin: true, SkipPixel: true}}
	if m["PROXY_LIST"] != "" {
		c.Pool = proxies.NewSource(func() (string, error) { v, e := db.Settings(); return v["PROXY_LIST"], e })
	}
	return ana.New(c), c.MemberNumber != "" && c.Password != "" && m["HYPER_API_KEY"] != "", nil
}
