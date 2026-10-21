package web

import (
	"awarddesk/internal/airports"
	"awarddesk/internal/award"
	"awarddesk/internal/jobs"
	"awarddesk/internal/login"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed assets/*
var assets embed.FS

type Server struct {
	Admin        *Admin
	Availability func() (bool, bool)
	Jobs         *jobs.Service
	Airports     []airports.Airport
	Ready        bool
	ProxyEnabled bool
	ANALogin     *login.Service
	EVALogin     *login.Service
	csrf         string
}

func New(j *jobs.Service, ready, proxy bool) (*Server, error) {
	a, e := airports.Load()
	if e != nil {
		return nil, e
	}
	b := make([]byte, 32)
	if _, e = rand.Read(b); e != nil {
		return nil, e
	}
	return &Server{Jobs: j, Airports: a, Ready: ready, ProxyEnabled: proxy, csrf: hex.EncodeToString(b)}, nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.adminRoutes(mux)
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		ready, proxy := s.Ready, s.ProxyEnabled
		if s.Availability != nil {
			ready, proxy = s.Availability()
		}
		evaReady := s.EVALogin != nil && s.EVALogin.Ready()
		if s.Admin != nil {
			evaReady = s.Admin.Fleet.Ready()
		}
		writeJSON(w, 200, map[string]any{"csrf": s.csrf, "ready": ready, "proxyEnabled": proxy, "providers": s.Jobs.Capabilities(), "anaLoginReady": s.ANALogin != nil && s.ANALogin.Ready(), "evaLoginReady": evaReady})
	})
	mux.HandleFunc("GET /api/logins/eva", func(w http.ResponseWriter, r *http.Request) {
		if s.EVALogin == nil {
			fail(w, 503, "EVA login is not configured")
			return
		}
		writeJSON(w, 200, s.EVALogin.Status())
	})
	mux.HandleFunc("POST /api/logins/eva", func(w http.ResponseWriter, r *http.Request) {
		if s.EVALogin == nil || !s.EVALogin.Ready() || (s.Admin != nil && !s.Admin.Fleet.Ready()) {
			fail(w, 503, "Configure EVA accounts, Outlook OAuth, proxies and solver keys in Admin")
			return
		}
		if err := s.EVALogin.Start(); err != nil {
			fail(w, 429, err.Error())
			return
		}
		writeJSON(w, 202, s.EVALogin.Status())
	})
	mux.HandleFunc("DELETE /api/logins/eva", func(w http.ResponseWriter, r *http.Request) {
		if s.EVALogin != nil {
			s.EVALogin.Cancel()
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /api/logins/ana", func(w http.ResponseWriter, r *http.Request) {
		if s.ANALogin == nil {
			fail(w, 503, "ANA login is not configured")
			return
		}
		writeJSON(w, 200, s.ANALogin.Status())
	})
	mux.HandleFunc("POST /api/logins/ana", func(w http.ResponseWriter, r *http.Request) {
		if s.ANALogin == nil || !s.ANALogin.Ready() {
			fail(w, 503, "Configure ANA_MEMBER_NUMBER, ANA_PASSWORD and HYPER_API_KEY, then restart")
			return
		}
		if err := s.ANALogin.Start(); err != nil {
			fail(w, 429, err.Error())
			return
		}
		writeJSON(w, 202, s.ANALogin.Status())
	})
	mux.HandleFunc("DELETE /api/logins/ana", func(w http.ResponseWriter, r *http.Request) {
		if s.ANALogin != nil {
			s.ANALogin.Cancel()
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /api/airports", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, airports.Search(s.Airports, r.URL.Query().Get("q")))
	})
	mux.HandleFunc("POST /api/searches", s.submit)
	mux.HandleFunc("GET /api/searches/{id}", func(w http.ResponseWriter, r *http.Request) {
		j, ok := s.Jobs.Get(r.PathValue("id"))
		if !ok {
			fail(w, 404, "search not found")
			return
		}
		writeJSON(w, 200, j)
	})
	mux.HandleFunc("DELETE /api/searches/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !s.Jobs.Cancel(r.PathValue("id")) {
			fail(w, 404, "search not found")
			return
		}
		w.WriteHeader(204)
	})
	root, _ := fs.Sub(assets, "assets")
	mux.Handle("GET /", http.FileServer(http.FS(root)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, e := url.Parse("http://" + r.Host)
		if e != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
			fail(w, 403, "local access only")
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			fail(w, 403, "cross-origin requests are not allowed")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != "GET" && r.Method != "HEAD" {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(s.csrf)) != 1 {
				fail(w, 403, "invalid request token")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	ready := s.Ready
	if s.Availability != nil {
		ready, _ = s.Availability()
	}
	if !ready {
		fail(w, 503, "Configure an airline account, proxies and solver settings in Admin")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		fail(w, 415, "JSON required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	var q award.Query
	if e := d.Decode(&q); e != nil {
		fail(w, 400, "invalid search request")
		return
	}
	if e := d.Decode(&struct{}{}); !errors.Is(e, io.EOF) {
		fail(w, 400, "one JSON object required")
		return
	}
	q.Origin = strings.ToUpper(strings.TrimSpace(q.Origin))
	q.Destination = strings.ToUpper(strings.TrimSpace(q.Destination))
	if e := q.Validate(time.Now()); e != nil {
		fail(w, 400, e.Error())
		return
	}
	j, e := s.Jobs.Submit(q)
	if e != nil {
		var ae *award.Error
		if errors.As(e, &ae) {
			writeJSON(w, 429, ae)
		} else {
			fail(w, 400, e.Error())
		}
		return
	}
	writeJSON(w, 202, j)
}
