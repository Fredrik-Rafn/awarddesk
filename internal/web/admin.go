package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"awarddesk/internal/fleet"
	"awarddesk/internal/jobs"
	"awarddesk/internal/proxies"
	"awarddesk/internal/registration"
	"awarddesk/internal/storage"
)

type Admin struct {
	DB            *storage.Store
	Fleet         *fleet.Fleet
	Registrations *registration.Service
	Searches      *jobs.Service
	mu            sync.Mutex
	failures      int
	next          time.Time
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
		case "/api/admin/registrations":
			a.registrations(w, r)
			return
		case "/api/admin/registrations/retry":
			if r.Method == "POST" {
				a.retryRegistration(w, r)
				return
			}
		case "/api/admin/registrations/retry-all":
			if r.Method == "POST" {
				a.retryAllRegistrations(w, r)
				return
			}
		case "/api/admin/registrations/restart":
			if r.Method == "POST" {
				a.restartRegistration(w, r)
				return
			}
		case "/api/admin/history":
			if r.Method == "GET" {
				s.adminHistory(w, r)
				return
			}
		case "/api/admin/analytics":
			if r.Method == "GET" {
				s.adminAnalytics(w, r)
				return
			}
		case "/api/admin/search":
			if r.Method == "GET" {
				s.adminSearchDetail(w, r)
				return
			}
		case "/api/admin/export":
			if r.Method == "GET" {
				s.adminExport(w, r)
				return
			}
		case "/api/admin/overview":
			if r.Method == "GET" {
				s.adminOverview(w, r)
				return
			}
		case "/api/admin/cache-policy":
			if r.Method == "GET" {
				p, e := a.DB.CachePolicy()
				if e != nil {
					fail(w, 500, "Could not read cache policy")
					return
				}
				writeJSON(w, 200, p)
				return
			}
			if r.Method == "PUT" {
				a.cachePolicy(w, r)
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
			if r.Method == "GET" {
				rows, e := a.DB.Accounts()
				if e != nil {
					fail(w, 500, "Could not read account")
					return
				}
				for _, row := range rows {
					if row.ID == r.URL.Query().Get("id") {
						writeJSON(w, 200, row)
						return
					}
				}
				fail(w, 404, "Account not found")
				return
			}
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
				a.Fleet.ProxyListChanged()
				writeJSON(w, 200, map[string]bool{"saved": true})
				return
			}
		case "/api/admin/prepare":
			if r.Method == "POST" {
				a.prepare(w, r)
				return
			}
		case "/api/admin/prepare-all":
			if r.Method == "POST" {
				result, e := a.Fleet.RequestPrepareAll()
				if e != nil {
					fail(w, 409, e.Error())
					return
				}
				writeJSON(w, 202, result)
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
		http.SetCookie(w, &http.Cookie{Name: "awarddesk_admin", Value: "", Path: "/api/admin", HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteStrictMode, MaxAge: -1})
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
		http.SetCookie(w, &http.Cookie{Name: "awarddesk_admin", Value: token, Path: "/api/admin", HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteStrictMode, MaxAge: 43200})
		writeJSON(w, 200, map[string]bool{"authenticated": true})
	default:
		fail(w, 405, "Method not allowed")
	}
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
