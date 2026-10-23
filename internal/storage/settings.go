package storage

func EditableSetting(key string) bool {
	switch key {
	case "HYPER_API_KEY", "ANYSOLVER_API_KEY", "EVA_IMPERVA_SOLVER",
		"EVA_BACKGROUND_ENABLED", "EVA_RESULT_CACHE_TTL", "EVA_SESSION_MAX_AGE",
		"SEARCH_CONCURRENCY", "FLIGHT_CACHE_ENABLED":
		return true
	}
	return false
}

func (s *Store) removeRetiredConfiguration() error {
	settings, err := s.Settings()
	if err != nil {
		return err
	}
	for key := range settings {
		if EditableSetting(key) || key == "PROXY_LIST" || len(key) > 0 && key[0] == '_' {
			continue
		}
		if _, err = s.DB.Exec("DELETE FROM settings WHERE key=?", key); err != nil {
			return err
		}
	}
	_, err = s.DB.Exec(`DELETE FROM states WHERE key LIKE '.state/%'
 AND key NOT LIKE '.state/eva/%' AND key NOT LIKE '.state/eva-results/%'
 AND key NOT LIKE '.state/outlook/%'`)
	return err
}
