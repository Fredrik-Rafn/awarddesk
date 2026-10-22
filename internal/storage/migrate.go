package storage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ImportLegacy is idempotent: after the first successful import the database
// wins over stale .env files, including when an administrator clears a value.
func (s *Store) ImportLegacy(root string) error {
	if s.Setting("_IMPORTED") != "" {
		return nil
	}
	values := map[string]string{}
	for _, key := range []string{"HYPER_API_KEY", "ANYSOLVER_API_KEY", "AWARDDESK_PROXY", "BA_EMAIL", "BA_PASSWORD", "ANA_MEMBER_NUMBER", "ANA_PASSWORD"} {
		values[key] = ""
	}
	if f, e := os.Open(filepath.Join(root, ".env")); e == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			k, _, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
			if ok {
				k = strings.TrimSpace(k)
				values[k] = os.Getenv(k)
			}
		}
		e = sc.Err()
		f.Close()
		if e != nil {
			return e
		}
	}
	for _, k := range []string{"EVA_EMAIL", "EVA_PASSWORD", "OUTLOOK_EMAIL", "OUTLOOK_CLIENT_ID", "OUTLOOK_REFRESH_TOKEN", "HYPER_API_KEY", "ANYSOLVER_API_KEY", "EVA_IMPERVA_SOLVER", "AWARDDESK_PROXY_FILE", "AWARDDESK_PROXY", "BA_EMAIL", "BA_PASSWORD", "ANA_MEMBER_NUMBER", "ANA_PASSWORD", "ANA_BACKGROUND_ENABLED", "EVA_BACKGROUND_ENABLED"} {
		if v, ok := os.LookupEnv(k); ok {
			values[k] = v
		}
	}
	values["EVA_RESULT_CACHE_TTL"] = "30m"
	values["SEARCH_CONCURRENCY"] = "4"
	values["EVA_SESSION_MAX_AGE"] = "3h"
	if values["EVA_BACKGROUND_ENABLED"] == "" {
		values["EVA_BACKGROUND_ENABLED"] = "true"
	}
	if values["EVA_IMPERVA_SOLVER"] == "" {
		values["EVA_IMPERVA_SOLVER"] = "auto"
	}
	proxyPath := values["AWARDDESK_PROXY_FILE"]
	if proxyPath == "" {
		proxyPath = "proxies.txt"
	}
	if !filepath.IsAbs(proxyPath) {
		proxyPath = filepath.Join(root, proxyPath)
	}
	if b, e := os.ReadFile(proxyPath); e == nil {
		values["PROXY_LIST"] = string(b)
	} else if values["AWARDDESK_PROXY_FILE"] != "" {
		return e
	}
	hash, e := bcrypt.GenerateFromPassword([]byte("admin1234"), bcrypt.DefaultCost)
	if e != nil {
		return e
	}
	values["_ADMIN_HASH"] = string(hash)
	type importedState struct {
		key string
		b   []byte
	}
	var states []importedState
	e = filepath.WalkDir(filepath.Join(root, ".state"), func(path string, d os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if len(b) > 8<<20 || !json.Valid(b) {
			return nil
		}
		key, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		states = append(states, importedState{Key(key), b})
		return nil
	})
	if e != nil {
		return e
	}
	delete(values, "HISTORY_RETENTION_DAYS")
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for k, v := range values {
		if _, e = tx.Exec("INSERT OR IGNORE INTO settings VALUES(?,?)", k, v); e != nil {
			return e
		}
	}
	if values["EVA_EMAIL"] != "" {
		a := Account{ID: ID(), Label: "EVA account 1", Email: values["EVA_EMAIL"], Password: values["EVA_PASSWORD"], OutlookEmail: values["OUTLOOK_EMAIL"], ClientID: values["OUTLOOK_CLIENT_ID"], RefreshToken: values["OUTLOOK_REFRESH_TOKEN"], Enabled: true, Revision: time.Now().UnixNano()}
		b, _ := json.Marshal(a)
		if _, e = tx.Exec("INSERT OR IGNORE INTO accounts VALUES(?,?,?)", a.ID, a.Email, b); e != nil {
			return e
		}
	}
	for _, v := range states {
		if _, e = tx.Exec("INSERT OR IGNORE INTO states VALUES(?,?,?)", v.key, v.b, time.Now().UnixMilli()); e != nil {
			return e
		}
	}
	for _, k := range []string{"EVA_EMAIL", "EVA_PASSWORD", "OUTLOOK_EMAIL", "OUTLOOK_CLIENT_ID", "OUTLOOK_REFRESH_TOKEN", "AWARDDESK_PROXY_FILE"} {
		if _, e = tx.Exec("DELETE FROM settings WHERE key=?", k); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("INSERT INTO settings VALUES('_IMPORTED',?)", time.Now().UTC().Format(time.RFC3339)); e != nil {
		return e
	}
	return tx.Commit()
}
