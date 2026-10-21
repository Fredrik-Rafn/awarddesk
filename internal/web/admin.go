package web

import (
	"awarddesk/internal/fleet"
	"awarddesk/internal/proxies"
	"awarddesk/internal/storage"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"io"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Admin struct {
	DB       *storage.Store
	Fleet    *fleet.Fleet
	mu       sync.Mutex
	failures int
	next     time.Time
}

func tokenHash(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func (a *Admin) authenticated(r *http.Request) bool {
	c, e := r.Cookie("awarddesk_admin")
	if e != nil || len(c.Value) != 32 {
		return false
	}
	var expires int64
	e = a.DB.DB.QueryRow("SELECT expires FROM admin_sessions WHERE token=?", tokenHash(c.Value)).Scan(&expires)
	return e == nil && time.Now().UnixMilli() < expires
}
func decodeAdmin(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return errors.New("invalid request")
	}
	if e := d.Decode(&struct{}{}); !errors.Is(e, io.EOF) {
		return errors.New("one JSON object required")
	}
	return nil
}
func (s *Server) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		b, _ := assets.ReadFile("assets/admin.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})
	handler := func(w http.ResponseWriter, r *http.Request) {
		a := s.Admin
		if a == nil {
			fail(w, 503, "Admin storage is unavailable")
			return
		}
		if r.URL.Path == "/api/admin/session" {
			a.session(w, r)
			return
		}
		if !a.authenticated(r) {
			fail(w, 401, "Admin login required")
			return
		}
		switch r.URL.Path {
		case "/api/admin/overview":
			if r.Method == "GET" {
				s.adminOverview(w, r)
				return
			}
		case "/api/admin/settings":
			if r.Method == "GET" {
				a.settings(w)
				return
			}
			if r.Method == "PUT" {
				a.putSettings(w, r)
				return
			}
		case "/api/admin/accounts":
			if r.Method == "PUT" {
				a.putAccount(w, r)
				return
			}
			if r.Method == "DELETE" {
				a.deleteAccount(w, r)
				return
			}
		case "/api/admin/proxies":
			if r.Method == "GET" {
				writeJSON(w, 200, map[string]string{"value": a.DB.Setting("PROXY_LIST")})
				return
			}
			if r.Method == "PUT" {
				var b struct {
					Value string `json:"value"`
				}
				if decodeAdmin(w, r, &b) != nil {
					fail(w, 400, "Invalid proxy list")
					return
				}
				if strings.TrimSpace(b.Value) != "" {
					if e := proxies.Validate(b.Value); e != nil {
						fail(w, 400, e.Error())
						return
					}
				}
				if a.DB.SetSettings(map[string]string{"PROXY_LIST": b.Value}) != nil {
					fail(w, 500, "Could not save proxies")
					return
				}
				writeJSON(w, 200, map[string]bool{"saved": true})
				return
			}
		case "/api/admin/prepare":
			if r.Method == "POST" {
				a.prepare(w, r)
				return
			}
		case "/api/admin/invalidate":
			if r.Method == "POST" {
				a.invalidate(w, r)
				return
			}
		case "/api/admin/cache":
			if r.Method == "GET" {
				key := r.URL.Query().Get("key")
				if !strings.HasPrefix(key, ".state/eva-results/") {
					fail(w, 400, "Invalid flight cache key")
					return
				}
				b, e := a.DB.ReadState(key)
				if e != nil {
					fail(w, 404, "Cached flight result not found")
					return
				}
				var record struct{ Result json.RawMessage }
				if json.Unmarshal(b, &record) != nil {
					fail(w, 500, "Invalid cached flight result")
					return
				}
				writeJSON(w, 200, record.Result)
				return
			}
			if r.Method == "DELETE" {
				key := r.URL.Query().Get("key")
				if key == "" {
					_, e := a.DB.DB.Exec("DELETE FROM states WHERE key LIKE '.state/eva-results/%'")
					if e != nil {
						fail(w, 500, "Could not clear cached flights")
						return
					}
				} else {
					if !strings.HasPrefix(key, ".state/eva-results/") {
						fail(w, 400, "Invalid flight cache key")
						return
					}
					if a.DB.DeleteState(key) != nil {
						fail(w, 500, "Could not remove cached flights")
						return
					}
				}
				w.WriteHeader(204)
				return
			}
		case "/api/admin/password":
			if r.Method == "PUT" {
				a.password(w, r)
				return
			}
		}
		fail(w, 404, "Admin action not found")
	}
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		mux.HandleFunc(method+" /api/admin/", handler)
	}
}
func (a *Admin) session(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		writeJSON(w, 200, map[string]bool{"authenticated": a.authenticated(r)})
	case "DELETE":
		if c, e := r.Cookie("awarddesk_admin"); e == nil {
			_, _ = a.DB.DB.Exec("DELETE FROM admin_sessions WHERE token=?", tokenHash(c.Value))
		}
		http.SetCookie(w, &http.Cookie{Name: "awarddesk_admin", Value: "", Path: "/api/admin", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		w.WriteHeader(204)
	case "POST":
		a.mu.Lock()
		defer a.mu.Unlock()
		if time.Now().Before(a.next) {
			fail(w, 429, "Too many attempts; wait one minute")
			return
		}
		var b struct {
			Password string `json:"password"`
		}
		if decodeAdmin(w, r, &b) != nil || len(b.Password) > 72 {
			fail(w, 400, "Invalid password request")
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(a.DB.Setting("_ADMIN_HASH")), []byte(b.Password)) != nil {
			a.failures++
			if a.failures >= 10 {
				a.next = time.Now().Add(time.Minute)
				a.failures = 0
			}
			fail(w, 401, "Incorrect password")
			return
		}
		a.failures = 0
		token := storage.ID()
		_, e := a.DB.DB.Exec("INSERT INTO admin_sessions VALUES(?,?)", tokenHash(token), time.Now().Add(12*time.Hour).UnixMilli())
		if e != nil {
			fail(w, 500, "Could not start admin session")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "awarddesk_admin", Value: token, Path: "/api/admin", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
		writeJSON(w, 200, map[string]bool{"authenticated": true})
	default:
		fail(w, 405, "Method not allowed")
	}
}
func secretSetting(k string) bool {
	return strings.Contains(k, "KEY") || strings.Contains(k, "PASSWORD") || strings.Contains(k, "TOKEN") || k == "AWARDDESK_PROXY"
}
func (a *Admin) settings(w http.ResponseWriter) {
	m, e := a.DB.Settings()
	if e != nil {
		fail(w, 500, "Could not read settings")
		return
	}
	type setting struct {
		Value      string `json:"value"`
		Secret     bool   `json:"secret"`
		Configured bool   `json:"configured"`
	}
	out := map[string]setting{}
	for k, v := range m {
		if strings.HasPrefix(k, "_") || k == "PROXY_LIST" {
			continue
		}
		row := setting{Value: v, Secret: secretSetting(k), Configured: v != ""}
		if row.Secret {
			row.Value = ""
		}
		out[k] = row
	}
	writeJSON(w, 200, out)
}

var settingName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,79}$`)

func validateSetting(k, v string) error {
	if !settingName.MatchString(k) || k == "PROXY_LIST" {
		return errors.New("Invalid setting name")
	}
	switch k {
	case "EVA_RESULT_CACHE_TTL":
		d, e := time.ParseDuration(v)
		if e != nil || d < 0 || d > 24*time.Hour {
			return errors.New("Cache duration must be between 0s and 24h")
		}
	case "EVA_SESSION_MAX_AGE":
		d, e := time.ParseDuration(v)
		if e != nil || d < 10*time.Minute || d > 24*time.Hour {
			return errors.New("Session maximum must be between 10m and 24h")
		}
	case "SEARCH_CONCURRENCY":
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 16 {
			return errors.New("Concurrency must be 1 to 16")
		}
	case "EVA_IMPERVA_SOLVER":
		if v != "auto" && v != "hyper" && v != "anysolver" {
			return errors.New("Solver must be auto, hyper or anysolver")
		}
	case "EVA_BACKGROUND_ENABLED", "ANA_BACKGROUND_ENABLED":
		if v != "true" && v != "false" {
			return errors.New("Background settings must be true or false")
		}
	case "AWARDDESK_DB":
		return errors.New("Database path is a bootstrap environment option, not a runtime setting")
	case "EVA_EMAIL", "EVA_PASSWORD", "OUTLOOK_EMAIL", "OUTLOOK_CLIENT_ID", "OUTLOOK_REFRESH_TOKEN", "AWARDDESK_PROXY_FILE":
		return errors.New("Manage EVA credentials in Accounts and the proxy list in Proxies")
	}
	return nil
}
func (a *Admin) putSettings(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Values map[string]string `json:"values"`
		Clear  []string          `json:"clear"`
	}
	if decodeAdmin(w, r, &b) != nil {
		fail(w, 400, "Invalid settings")
		return
	}
	if b.Values == nil {
		b.Values = map[string]string{}
	}
	for _, k := range b.Clear {
		if !secretSetting(k) {
			fail(w, 400, "Only secret fields support explicit clearing")
			return
		}
		b.Values[k] = ""
	}
	for k, v := range b.Values {
		if e := validateSetting(k, v); e != nil {
			fail(w, 400, e.Error())
			return
		}
	}
	if a.DB.SetSettings(b.Values) != nil {
		fail(w, 500, "Could not save settings")
		return
	}
	writeJSON(w, 200, map[string]bool{"saved": true})
}
func (a *Admin) putAccount(w http.ResponseWriter, r *http.Request) {
	var v storage.Account
	if decodeAdmin(w, r, &v) != nil {
		fail(w, 400, "Invalid account")
		return
	}
	v.Email = strings.TrimSpace(v.Email)
	v.OutlookEmail = strings.TrimSpace(v.OutlookEmail)
	if _, e := mail.ParseAddress(v.Email); e != nil {
		fail(w, 400, "Enter a valid EVA email")
		return
	}
	if _, e := mail.ParseAddress(v.OutlookEmail); e != nil {
		fail(w, 400, "Enter a valid Outlook email")
		return
	}
	if len(v.Label) > 120 || len(v.Password) > 512 || len(v.RefreshToken) > 16384 || len(v.ClientID) > 200 {
		fail(w, 400, "Account field is too long")
		return
	}
	old := storage.Account{}
	rows, e := a.DB.Accounts()
	if e != nil {
		fail(w, 500, "Could not read accounts")
		return
	}
	for _, row := range rows {
		if row.ID == v.ID {
			old = row
		}
	}
	if v.ID != "" && old.ID == "" {
		fail(w, 404, "Account not found")
		return
	}
	if v.ID == "" {
		v.ID = storage.ID()
	}
	if old.ID != "" {
		if !strings.EqualFold(old.Email, v.Email) {
			fail(w, 400, "Create a new account to change its EVA email")
			return
		}
		if v.Password == "" {
			v.Password = old.Password
		}
		if v.RefreshToken == "" {
			v.RefreshToken = old.RefreshToken
		}
	}
	if v.Password == "" || v.RefreshToken == "" || v.ClientID == "" {
		fail(w, 400, "EVA password, Outlook client ID and refresh token are required")
		return
	}
	e = a.Fleet.Edit(v.ID, func() error {
		if old.RefreshToken != "" && (old.RefreshToken != v.RefreshToken || old.ClientID != v.ClientID || old.OutlookEmail != v.OutlookEmail) {
			if e := a.DB.DeleteState(storage.MailboxKey(old.OutlookEmail, old.ClientID)); e != nil {
				return e
			}
		}
		if old.Password != "" && old.Password != v.Password {
			if e := a.DB.DeleteState(storage.AccountKey(old.Email)); e != nil {
				return e
			}
		}
		if e := a.DB.PutAccount(v); e != nil {
			return e
		}
		return a.DB.Runtime("account:"+v.ID, fleet.Status{ID: v.ID, State: "idle"})
	})
	if e != nil {
		fail(w, 409, "Could not save account; account may be busy or email already exists")
		return
	}
	writeJSON(w, 200, map[string]bool{"saved": true})
}
func (a *Admin) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	rows, _ := a.DB.Accounts()
	for _, v := range rows {
		if v.ID == id {
			e := a.Fleet.Edit(id, func() error {
				if e := a.DB.DeleteState(storage.AccountKey(v.Email)); e != nil {
					return e
				}
				shared := false
				for _, other := range rows {
					if other.ID != id && strings.EqualFold(other.OutlookEmail, v.OutlookEmail) && other.ClientID == v.ClientID {
						shared = true
					}
				}
				if !shared {
					if e := a.DB.DeleteState(storage.MailboxKey(v.OutlookEmail, v.ClientID)); e != nil {
						return e
					}
				}
				return a.DB.DeleteAccount(id)
			})
			if e != nil {
				fail(w, 409, "Account is busy or could not be removed")
				return
			}
			w.WriteHeader(204)
			return
		}
	}
	fail(w, 404, "Account not found")
}
func (a *Admin) prepare(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID string `json:"id"`
	}
	if decodeAdmin(w, r, &b) != nil || b.ID == "" {
		fail(w, 400, "Account ID required")
		return
	}
	rows, _ := a.DB.Accounts()
	found := false
	for _, v := range rows {
		if v.ID == b.ID && v.Enabled {
			found = true
		}
	}
	if !found {
		fail(w, 404, "Enabled account not found")
		return
	}
	if e := a.Fleet.RequestPrepare(b.ID); e != nil {
		fail(w, 409, e.Error())
		return
	}
	writeJSON(w, 202, map[string]bool{"started": true})
}
func (a *Admin) invalidate(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID string `json:"id"`
	}
	if decodeAdmin(w, r, &b) != nil {
		fail(w, 400, "Account ID required")
		return
	}
	rows, _ := a.DB.Accounts()
	for _, v := range rows {
		if v.ID == b.ID {
			e := a.Fleet.Edit(v.ID, func() error { return a.DB.DeleteState(storage.AccountKey(v.Email)) })
			if e != nil {
				fail(w, 409, "Account is busy or session could not be removed")
				return
			}
			w.WriteHeader(204)
			return
		}
	}
	fail(w, 404, "Account not found")
}
func (a *Admin) password(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Password string `json:"password"`
	}
	if decodeAdmin(w, r, &b) != nil || len(b.Password) < 4 || len(b.Password) > 72 {
		fail(w, 400, "Password must contain 4 to 72 bytes")
		return
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(b.Password), bcrypt.DefaultCost)
	if e != nil || a.DB.SetSettings(map[string]string{"_ADMIN_HASH": string(hash)}) != nil {
		fail(w, 500, "Could not change password")
		return
	}
	_, _ = a.DB.DB.Exec("DELETE FROM admin_sessions")
	writeJSON(w, 200, map[string]bool{"saved": true})
}
func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	a := s.Admin
	accounts, e := a.DB.Accounts()
	if e != nil {
		fail(w, 500, "Could not read accounts")
		return
	}
	for i := range accounts {
		accounts[i].Password = ""
		accounts[i].RefreshToken = ""
	}
	statuses, e := a.Fleet.Statuses()
	if e != nil {
		fail(w, 500, "Could not read sessions")
		return
	}
	flights, e := a.DB.CachedFlights()
	if e != nil {
		fail(w, 500, "Could not read saved flights")
		return
	}
	history, e := a.DB.JobSummaries()
	if e != nil {
		fail(w, 500, "Could not read search history")
		return
	}
	proxyCount := 0
	for _, line := range strings.Split(a.DB.Setting("PROXY_LIST"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			proxyCount++
		}
	}
	writeJSON(w, 200, map[string]any{"proxyCount": proxyCount, "accounts": accounts, "sessions": statuses, "flights": flights, "jobs": history, "now": time.Now().UTC(), "ready": a.Fleet.Ready()})
}
