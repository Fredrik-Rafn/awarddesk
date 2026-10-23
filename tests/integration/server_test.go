package integration

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"awarddesk/internal/award"
	"awarddesk/internal/jobs"
	"awarddesk/internal/login"
	"awarddesk/internal/web"
)

func TestLocalBoundary(t *testing.T) {
	j := jobs.New()
	defer j.Close()
	s, e := web.New(j, true, false)
	if e != nil {
		t.Fatal(e)
	}
	h := s.Handler()
	cases := []struct {
		name, method, path, host, origin, token, body string
		status                                        int
	}{
		{"configuration", "GET", "/api/config", "localhost:8080", "", "", "", 200},
		{"rebinding", "GET", "/api/config", "evil.example", "", "", "", 403},
		{"cross origin", "GET", "/api/config", "localhost:8080", "https://evil.example", "", "", 403},
		{"missing csrf", "POST", "/api/searches", "localhost:8080", "", "", "{}", 403},
		{"invalid fields", "POST", "/api/searches", "localhost:8080", "", requestToken(s), `{"secret":"no"}`, 400},
		{"oversized", "POST", "/api/searches", "localhost:8080", "", requestToken(s), strings.Repeat(" ", 9000), 400},
		{"unknown job", "GET", "/api/searches/unknown", "localhost:8080", "", "", "", 404},
		{"airport", "GET", "/api/airports?q=JFK", "localhost:8080", "", "", "", 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-CSRF-Token", tc.token)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if tc.status == 200 && w.Header().Get("Content-Security-Policy") == "" {
				t.Fatal("security policy missing")
			}
		})
	}
}

func TestEVALoginLifecycle(t *testing.T) {
	j := jobs.New()
	defer j.Close()
	s, err := web.New(j, false, true)
	if err != nil {
		t.Fatal(err)
	}
	s.EVALogin = login.New(func(ctx context.Context, _ award.Reporter) error { <-ctx.Done(); return ctx.Err() })
	defer s.EVALogin.Close()
	h := s.Handler()
	for _, tc := range []struct {
		method, token string
		code          int
	}{{"POST", "", 403}, {"POST", requestToken(s), 202}, {"POST", requestToken(s), 202}, {"GET", "", 200}, {"DELETE", "", 403}, {"DELETE", requestToken(s), 204}} {
		r := httptest.NewRequest(tc.method, "http://localhost:8080/api/logins/eva", nil)
		r.Header.Set("X-CSRF-Token", tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("%s: got %d", tc.method, w.Code)
		}
	}
}
