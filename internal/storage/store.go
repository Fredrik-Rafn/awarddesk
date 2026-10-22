// Package storage owns local configuration, portable sessions and search history.
package storage

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"awarddesk/internal/sessionstore"
)

type Store struct {
	DB   *sql.DB
	path string
}
type Account struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Email        string `json:"email"`
	Password     string `json:"password,omitempty"`
	OutlookEmail string `json:"outlookEmail"`
	ClientID     string `json:"clientId"`
	RefreshToken string `json:"refreshToken,omitempty"`
	Enabled      bool   `json:"enabled"`
	Revision     int64  `json:"revision"`
}
type State struct {
	Key       string          `json:"key"`
	UpdatedAt time.Time       `json:"updatedAt"`
	Size      int             `json:"size"`
	Data      json.RawMessage `json:"-"`
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.ToSlash(path)+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{DB: db, path: path}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS accounts(id TEXT PRIMARY KEY,email TEXT NOT NULL UNIQUE COLLATE NOCASE,data BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS states(key TEXT PRIMARY KEY,data BLOB NOT NULL,updated_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS leases(key TEXT PRIMARY KEY,owner TEXT NOT NULL,expires INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS jobs(id TEXT PRIMARY KEY,data BLOB NOT NULL,updated_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS admin_sessions(token TEXT PRIMARY KEY,expires INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS runtime(key TEXT PRIMARY KEY,data BLOB NOT NULL);
 DELETE FROM settings WHERE key='HISTORY_RETENTION_DAYS';
 INSERT OR IGNORE INTO settings(key,value) VALUES('FLIGHT_CACHE_ENABLED','true');
 CREATE INDEX IF NOT EXISTS jobs_created_time ON jobs(julianday(json_extract(data,'$.createdAt')));
 CREATE INDEX IF NOT EXISTS jobs_status ON jobs(json_extract(data,'$.status'));
 CREATE INDEX IF NOT EXISTS jobs_updated ON jobs(updated_at);
 PRAGMA user_version=1;`)
	if err != nil {
		db.Close()
		return nil, err
	}
	if err = s.removeRetiredConfiguration(); err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0600)
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Settings() (map[string]string, error) {
	rows, e := s.DB.Query("SELECT key,value FROM settings")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if e = rows.Scan(&k, &v); e != nil {
			return nil, e
		}
		m[k] = v
	}
	return m, rows.Err()
}
func (s *Store) Setting(k string) string {
	var v string
	_ = s.DB.QueryRow("SELECT value FROM settings WHERE key=?", k).Scan(&v)
	return v
}
func (s *Store) SetSettings(m map[string]string) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for k, v := range m {
		if _, e = tx.Exec("INSERT INTO settings VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", k, v); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func ID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func (s *Store) Accounts() ([]Account, error) {
	rows, e := s.DB.Query("SELECT data FROM accounts ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	a := []Account{}
	for rows.Next() {
		var b []byte
		var v Account
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, e
		}
		a = append(a, v)
	}
	return a, rows.Err()
}
func (s *Store) PutAccount(a Account) error {
	if a.ID == "" {
		a.ID = ID()
	}
	a.Revision = time.Now().UnixNano()
	b, e := json.Marshal(a)
	if e != nil {
		return e
	}
	_, e = s.DB.Exec("INSERT INTO accounts VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET email=excluded.email,data=excluded.data", a.ID, a.Email, b)
	return e
}
func (s *Store) DeleteAccount(id string) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("DELETE FROM accounts WHERE id=?", id); e != nil {
		return e
	}
	if _, e = tx.Exec("DELETE FROM runtime WHERE key=?", "account:"+id); e != nil {
		return e
	}
	return tx.Commit()
}
func Key(k string) string { return strings.ReplaceAll(filepath.ToSlash(k), "\\", "/") }
func AccountKey(email string) string {
	h := sha256.Sum256([]byte(strings.ToLower(email)))
	return ".state/eva/" + hex.EncodeToString(h[:]) + ".json"
}
func MailboxKey(email, clientID string) string {
	h := sha256.Sum256([]byte(strings.ToLower(email) + "|" + clientID))
	return ".state/outlook/" + hex.EncodeToString(h[:]) + ".json"
}
func (s *Store) ReadState(k string) ([]byte, error) {
	var b []byte
	var e error
	if strings.HasPrefix(Key(k), ".state/eva-results/") {
		b, e = cacheStateQuery(s, Key(k))
	} else {
		e = s.DB.QueryRow("SELECT data FROM states WHERE key=?", Key(k)).Scan(&b)
	}
	if errors.Is(e, sql.ErrNoRows) {
		e = os.ErrNotExist
	}
	return b, e
}
func (s *Store) WriteState(k string, b []byte) error {
	if len(b) == 0 || len(b) > 8<<20 || !json.Valid(b) {
		return errors.New("invalid state")
	}
	query := "INSERT INTO states VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET data=excluded.data,updated_at=excluded.updated_at"
	if strings.HasPrefix(Key(k), ".state/eva-results/") {
		query = "INSERT INTO states SELECT ?,?,? WHERE " + cacheAllowedSQL + " ON CONFLICT(key) DO UPDATE SET data=excluded.data,updated_at=excluded.updated_at"
	}
	_, e := s.DB.Exec(query, Key(k), b, time.Now().UnixMilli())
	return e
}
func (s *Store) DeleteState(k string) error {
	_, e := s.DB.Exec("DELETE FROM states WHERE key=?", Key(k))
	return e
}

// An OS lock releases immediately on process exit, including crashes. SQLite
// records ownership for diagnostics; no SQL transaction spans network work.
func (s *Store) LockState(k string) (func(), error) {
	h := sha256.Sum256([]byte(Key(k)))
	lock := sessionstore.Store{Path: filepath.Join(s.path+".locks", hex.EncodeToString(h[:]))}
	release, e := lock.Lock()
	if e != nil {
		return nil, e
	}
	owner := ID()
	if _, e = s.DB.Exec("INSERT INTO leases VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET owner=excluded.owner,expires=excluded.expires", Key(k), owner, time.Now().Add(20*time.Minute).UnixMilli()); e != nil {
		release()
		return nil, e
	}
	var once sync.Once
	return func() {
		once.Do(func() { _, _ = s.DB.Exec("DELETE FROM leases WHERE key=? AND owner=?", Key(k), owner); release() })
	}, nil
}
func (s *Store) States() ([]State, error) {
	rows, e := s.DB.Query("SELECT key,data,updated_at FROM states ORDER BY updated_at DESC")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []State{}
	for rows.Next() {
		var v State
		var at int64
		if e = rows.Scan(&v.Key, &v.Data, &at); e != nil {
			return nil, e
		}
		v.UpdatedAt = time.UnixMilli(at)
		v.Size = len(v.Data)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) SaveJob(id string, b []byte) error {
	_, e := s.DB.Exec("INSERT INTO jobs VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data,updated_at=excluded.updated_at", id, b, time.Now().UnixMilli())
	return e
}
func (s *Store) LoadJob(id string) ([]byte, error) {
	var b []byte
	e := s.DB.QueryRow("SELECT data FROM jobs WHERE id=?", id).Scan(&b)
	return b, e
}
func (s *Store) Jobs() ([]json.RawMessage, error) {
	rows, e := s.DB.Query("SELECT data FROM jobs ORDER BY CASE WHEN json_extract(data,'$.status') IN ('running','queued') THEN 0 ELSE 1 END,updated_at DESC LIMIT 1000")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b json.RawMessage
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Store) Runtime(key string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = s.DB.Exec("INSERT INTO runtime VALUES(?,?) ON CONFLICT(key) DO UPDATE SET data=excluded.data", key, b)
	return e
}
func (s *Store) RuntimeValue(key string, v any) {
	var b []byte
	if s.DB.QueryRow("SELECT data FROM runtime WHERE key=?", key).Scan(&b) == nil {
		_ = json.Unmarshal(b, v)
	}
}
func (s *Store) Prune() error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, q := range []struct {
		sql string
		at  int64
	}{{"DELETE FROM admin_sessions WHERE expires<?", time.Now().UnixMilli()}, {"DELETE FROM leases WHERE expires<?", time.Now().UnixMilli()}} {
		if _, e = tx.Exec(q.sql, q.at); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	entries, e := s.CachedFlights()
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.Expired {
			if e = s.DeleteState(entry.Key); e != nil {
				return e
			}
		}
	}
	_, e = s.DB.Exec("DELETE FROM states WHERE key IN (SELECT key FROM states WHERE key LIKE '.state/eva-results/%' ORDER BY updated_at DESC LIMIT -1 OFFSET 1000)")
	return e
}
