package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const searchActivityKey = "search:last-submission"

type searchActivity struct {
	LastSubmittedAt int64 `json:"lastSubmittedAt"`
}

func (s *Store) SearchActivity() (time.Time, error) {
	var raw []byte
	if err := s.DB.QueryRow("SELECT data FROM runtime WHERE key=?", searchActivityKey).Scan(&raw); err != nil {
		return time.Time{}, err
	}
	var activity searchActivity
	if json.Unmarshal(raw, &activity) != nil || activity.LastSubmittedAt <= 0 {
		return time.Time{}, errors.New("invalid search activity")
	}
	return time.UnixMilli(activity.LastSubmittedAt).UTC(), nil
}

func (s *Store) InitializeSearchActivity(now time.Time) error {
	last, err := s.SearchActivity()
	existing := err == nil
	if err == nil && !last.After(now) {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if last.IsZero() {
		var created string
		err = s.DB.QueryRow(`SELECT json_extract(data,'$.createdAt') FROM jobs WHERE julianday(json_extract(data,'$.createdAt')) IS NOT NULL ORDER BY julianday(json_extract(data,'$.createdAt')) DESC LIMIT 1`).Scan(&created)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		last, _ = time.Parse(time.RFC3339Nano, created)
	}
	if last.IsZero() || last.After(now) {
		last = now
	}
	raw, _ := json.Marshal(searchActivity{LastSubmittedAt: last.UnixMilli()})
	if existing {
		_, err = s.DB.Exec("UPDATE runtime SET data=? WHERE key=?", raw, searchActivityKey)
	} else {
		_, err = s.DB.Exec("INSERT INTO runtime VALUES(?,?) ON CONFLICT(key) DO NOTHING", searchActivityKey, raw)
	}
	return err
}

func (s *Store) RecordSearchActivity(at time.Time) (time.Time, error) {
	previous, err := s.SearchActivity()
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, err
	}
	if !previous.IsZero() && !at.After(previous) {
		return previous, nil
	}
	raw, err := json.Marshal(searchActivity{LastSubmittedAt: at.UnixMilli()})
	if err != nil {
		return previous, err
	}
	_, err = s.DB.Exec(`INSERT INTO runtime VALUES(?,?) ON CONFLICT(key) DO UPDATE SET data=excluded.data WHERE json_extract(runtime.data,'$.lastSubmittedAt')<json_extract(excluded.data,'$.lastSubmittedAt')`, searchActivityKey, raw)
	return previous, err
}
