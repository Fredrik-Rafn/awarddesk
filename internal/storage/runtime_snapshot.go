package storage

import "encoding/json"

// RuntimeSettings avoids transferring the potentially multi-megabyte proxy list
// every time a worker checks account capacity. Only the pool reads that value.
func (s *Store) RuntimeSettings() (map[string]string, error) {
	rows, err := s.DB.Query("SELECT key,value FROM settings WHERE key!='PROXY_LIST'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		values[key] = value
	}
	return values, rows.Err()
}

func (s *Store) HasProxies() bool {
	var configured bool
	_ = s.DB.QueryRow("SELECT length(value)>0 FROM settings WHERE key='PROXY_LIST'").Scan(&configured)
	return configured
}

// AccountRuntime reads one consistent snapshot instead of one query per account.
func (s *Store) AccountRuntime() (map[string]json.RawMessage, error) {
	rows, err := s.DB.Query("SELECT key,data FROM runtime WHERE key LIKE 'account:%'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make(map[string]json.RawMessage)
	for rows.Next() {
		var key string
		var data []byte
		if err = rows.Scan(&key, &data); err != nil {
			return nil, err
		}
		values[key] = json.RawMessage(data)
	}
	return values, rows.Err()
}
