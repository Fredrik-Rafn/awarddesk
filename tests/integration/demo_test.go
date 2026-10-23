package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/jobs"
	"awarddesk/internal/web"
)

const demoAddress = "https://example.ngrok-free.app"
const demoPassword = "separate-test-password"

func TestDemoConfigurationFailsClosed(t *testing.T) {
	j := jobs.New()
	defer j.Close()
	for _, address := range []string{"http://example.com", "https://*.example.com", "https://localhost", "https://127.0.0.1", "https://user:pass@example.com", "https://example.com/path", "https://example.com?", "https://example.com#fragment", "https://example.com:", "https://example.com:99999", "https://example.com\\evil"} {
		s, err := web.New(j, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if s.ConfigureDemo(address, demoPassword) == nil {
			t.Fatalf("unsafe demo address accepted: %s", address)
		}
	}
	s, _ := web.New(j, false, false)
	if s.ConfigureDemo(demoAddress, "") == nil || s.ConfigureDemo(demoAddress, "admin1234") == nil {
		t.Fatal("demo mode accepted a missing/short separate password")
	}
}

func TestDemoCanSubmitAndPollSearchWithoutAnAirline(t *testing.T) {
	j := jobs.New(jobProvider{run: func(context.Context) (award.Result, error) {
		return award.Result{Complete: true, FetchedAt: time.Now()}, nil
	}})
	defer j.Close()
	s, err := web.New(j, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfigureDemo(demoAddress, demoPassword); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	request := func(method, path string, body []byte, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, demoAddress+path, bytes.NewReader(body))
		r.SetBasicAuth("demo", demoPassword)
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("Origin", demoAddress)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	config := request("GET", "/api/config", nil, "")
	var settings struct {
		CSRF string `json:"csrf"`
	}
	if config.Code != 200 || json.Unmarshal(config.Body.Bytes(), &settings) != nil {
		t.Fatal("public config failed")
	}
	query, _ := json.Marshal(jobQuery())
	w := request("POST", "/api/searches", query, settings.CSRF)
	var submitted jobs.Job
	if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &submitted) != nil {
		t.Fatalf("public submit: %d %s", w.Code, w.Body.String())
	}
	awaitJob(t, j, submitted.ID, "completed")
	w = request("GET", "/api/searches/"+submitted.ID, nil, "")
	var completed jobs.Job
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &completed) != nil || completed.Result == nil || !completed.Result.Complete {
		t.Fatal("public result polling failed")
	}
}

func TestDemoAccessKeepsOriginTokenAndLocalAdminBoundaries(t *testing.T) {
	s, _ := adminTestServer(t)
	if err := s.ConfigureDemo(demoAddress, demoPassword); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	localCookie := adminLogin(t, s, h)
	token := requestToken(s)
	type requestCase struct {
		name, host, path, method, origin, proto, password, csrf, forwardedHost string
		status                                                                 int
	}
	cases := []requestCase{
		{name: "public authentication required", host: "example.ngrok-free.app", path: "/api/config", proto: "https", status: 401},
		{name: "wrong password", host: "example.ngrok-free.app", path: "/api/config", proto: "https", password: "incorrect", status: 401},
		{name: "public config", host: "example.ngrok-free.app", path: "/api/config", proto: "https", password: demoPassword, status: 200},
		{name: "public page", host: "example.ngrok-free.app", path: "/", proto: "https", password: demoPassword, status: 200},
		{name: "public asset", host: "example.ngrok-free.app", path: "/app.js", proto: "https", password: demoPassword, status: 200},
		{name: "asset protected", host: "example.ngrok-free.app", path: "/app.js", proto: "https", status: 401},
		{name: "HTTP rejected", host: "example.ngrok-free.app", path: "/api/config", proto: "http", password: demoPassword, status: 403},
		{name: "wrong host", host: "evil.example", path: "/api/config", proto: "https", password: demoPassword, status: 403},
		{name: "forwarded host grants nothing", host: "evil.example", forwardedHost: "example.ngrok-free.app", path: "/api/config", proto: "https", password: demoPassword, status: 403},
		{name: "local host rewrite rejected", host: "localhost:8080", forwardedHost: "example.ngrok-free.app", path: "/api/admin/overview", proto: "https", password: demoPassword, status: 403},
		{name: "cross origin rejected", host: "example.ngrok-free.app", path: "/api/config", origin: "https://evil.example", proto: "https", password: demoPassword, status: 403},
		{name: "same origin accepted", host: "example.ngrok-free.app", path: "/api/config", origin: demoAddress, proto: "https", password: demoPassword, status: 200},
		{name: "missing CSRF rejected", host: "example.ngrok-free.app", path: "/api/searches/unknown", method: "DELETE", origin: demoAddress, proto: "https", password: demoPassword, status: 403},
		{name: "correct CSRF reaches handler", host: "example.ngrok-free.app", path: "/api/searches/unknown", method: "DELETE", origin: demoAddress, proto: "https", password: demoPassword, csrf: token, status: 404},
		{name: "local remains available", host: "localhost:8080", path: "/api/config", status: 200},
		{name: "local admin remains available", host: "localhost:8080", path: "/api/admin/overview", status: 200},
	}
	for _, path := range []string{"/admin", "/admin.html", "/admin/", "/api/admin/session", "/api/admin/accounts", "/api/logins/eva"} {
		cases = append(cases, requestCase{name: "private " + path, host: "example.ngrok-free.app", path: path, proto: "https", password: demoPassword, status: 403})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			method := tc.method
			if method == "" {
				method = "GET"
			}
			r := httptest.NewRequest(method, "http://"+tc.host+tc.path, nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-Forwarded-Proto", tc.proto)
			r.Header.Set("X-Forwarded-Host", tc.forwardedHost)
			r.Header.Set("X-CSRF-Token", tc.csrf)
			r.AddCookie(localCookie) // A valid admin cookie must not bypass the public boundary.
			if tc.password != "" {
				r.SetBasicAuth("demo", tc.password)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
			if tc.status == 401 && !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Basic ") {
				t.Fatal("browser password prompt missing")
			}
			if tc.path == "/api/config" && tc.status == 200 {
				var config struct {
					Demo bool `json:"demo"`
				}
				if json.Unmarshal(w.Body.Bytes(), &config) != nil || config.Demo != (tc.host == "example.ngrok-free.app") {
					t.Fatal("wrong demo UI mode")
				}
				if strings.Contains(w.Body.String(), demoPassword) {
					t.Fatal("demo password leaked")
				}
			}
		})
	}
}
