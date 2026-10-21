package storage

import (
	"encoding/json"
	"time"
)

type CachedFlight struct {
	Key       string          `json:"key"`
	Query     json.RawMessage `json:"query"`
	FetchedAt time.Time       `json:"fetchedAt"`
	ExpiresAt time.Time       `json:"expiresAt"`
	Expired   bool            `json:"expired"`
	Count     int             `json:"count"`
}

func (s *Store) CachedFlights() ([]CachedFlight, error) {
	ttl, _ := time.ParseDuration(s.Setting("EVA_RESULT_CACHE_TTL"))
	rows, e := s.DB.Query(`SELECT key,COALESCE(json_extract(data,'$.result.query'),'{}'),COALESCE(json_extract(data,'$.result.fetchedAt'),''),COALESCE(json_array_length(data,'$.result.itineraries'),0) FROM states WHERE key LIKE '.state/eva-results/%' ORDER BY updated_at DESC`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []CachedFlight{}
	for rows.Next() {
		var v CachedFlight
		var query, at string
		if e = rows.Scan(&v.Key, &query, &at, &v.Count); e != nil {
			return nil, e
		}
		v.Query = json.RawMessage(query)
		v.FetchedAt, _ = time.Parse(time.RFC3339Nano, at)
		v.ExpiresAt = v.FetchedAt.Add(ttl)
		v.Expired = !time.Now().Before(v.ExpiresAt)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) JobSummaries() ([]json.RawMessage, error) {
	rows, e := s.DB.Query(`SELECT json_remove(data,'$.result') FROM jobs ORDER BY CASE WHEN json_extract(data,'$.status') IN ('running','queued') THEN 0 ELSE 1 END,updated_at DESC LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b string
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		out = append(out, json.RawMessage(b))
	}
	return out, rows.Err()
}
