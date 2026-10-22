package storage

import (
	"database/sql"
	"errors"
	"time"
)

var ErrCacheConfirmation = errors.New("Disabling flight caching deletes every cached flight result; confirmation is required")

type CachePolicy struct {
	Enabled bool   `json:"enabled"`
	TTL     string `json:"ttl"`
}

func cachePolicy(values map[string]string) CachePolicy {
	ttl := values["EVA_RESULT_CACHE_TTL"]
	if ttl == "" {
		ttl = "30m"
	}
	d, e := time.ParseDuration(ttl)
	return CachePolicy{Enabled: values["FLIGHT_CACHE_ENABLED"] != "false" && e == nil && d > 0, TTL: ttl}
}
func (s *Store) CachePolicy() (CachePolicy, error) {
	m, e := s.RuntimeSettings()
	if e != nil {
		return CachePolicy{}, e
	}
	return cachePolicy(m), nil
}

const cacheAllowedSQL = `COALESCE((SELECT value FROM settings WHERE key='FLIGHT_CACHE_ENABLED'),'true')<>'false' AND COALESCE((SELECT value FROM settings WHERE key='EVA_RESULT_CACHE_TTL'),'30m') NOT IN ('0s','0','0m','0h')`

// Policy and deletion commit together. The cache write path checks the same
// setting in its INSERT statement, so an already-running search cannot repopulate
// the cache after it has been switched off. Job history is never deleted.
func (s *Store) ApplySettings(values map[string]string, confirmed bool) error {
	disabling := values["FLIGHT_CACHE_ENABLED"] == "false"
	if raw, ok := values["EVA_RESULT_CACHE_TTL"]; ok {
		if d, e := time.ParseDuration(raw); e == nil && d == 0 {
			disabling = true
			values["FLIGHT_CACHE_ENABLED"] = "false"
		}
	}
	if disabling && !confirmed {
		return ErrCacheConfirmation
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for k, v := range values {
		if _, e = tx.Exec("INSERT INTO settings VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", k, v); e != nil {
			return e
		}
	}
	if disabling {
		if _, e = tx.Exec("DELETE FROM states WHERE key LIKE '.state/eva-results/%'"); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) SetCachePolicy(enabled, confirmed bool) error {
	values := map[string]string{"FLIGHT_CACHE_ENABLED": "false"}
	if enabled {
		values["FLIGHT_CACHE_ENABLED"] = "true"
		p, e := s.CachePolicy()
		if e != nil {
			return e
		}
		if d, e := time.ParseDuration(p.TTL); e != nil || d <= 0 {
			values["EVA_RESULT_CACHE_TTL"] = "30m"
		}
	}
	return s.ApplySettings(values, confirmed)
}

func cacheStateQuery(s *Store, key string) ([]byte, error) {
	var b []byte
	e := s.DB.QueryRow("SELECT data FROM states WHERE key=? AND "+cacheAllowedSQL, key).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	return b, e
}
