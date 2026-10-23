package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"awarddesk/internal/fleet"
	"awarddesk/internal/jobs"
	"awarddesk/internal/storage"
	"awarddesk/internal/web"
)

func adminTestServer(t *testing.T) (*web.Server, http.Handler) {
	t.Helper()
	db, e := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	if e = db.ImportLegacy(t.TempDir()); e != nil {
		t.Fatal(e)
	}
	_ = db.SetSettings(map[string]string{"HYPER_API_KEY": "test-secret-key"})
	f := fleet.New(db)
	j := jobs.NewConcurrent(4, f)
	s, e := web.New(j, true, true)
	if e != nil {
		t.Fatal(e)
	}
	s.Admin = &web.Admin{DB: db, Fleet: f}
	t.Cleanup(func() { j.Close(); f.Close(); db.Close() })
	return s, s.Handler()
}
func adminRequest(h http.Handler, s *web.Server, method, path, body string, cookie *http.Cookie, csrf bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost:8080"+path, bytes.NewBufferString(body))
	r.Host = "localhost:8080"
	r.Header.Set("Origin", "http://localhost:8080")
	r.Header.Set("Content-Type", "application/json")
	if csrf {
		r.Header.Set("X-CSRF-Token", requestToken(s))
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func adminLogin(t *testing.T, s *web.Server, h http.Handler) *http.Cookie {
	t.Helper()
	w := adminRequest(h, s, "POST", "/api/admin/session", `{"password":"admin1234"}`, nil, true)
	if w.Code != 200 {
		t.Fatalf("login=%d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("insecure cookie")
	}
	return cookies[0]
}

func requestToken(s *web.Server) string {
	r := httptest.NewRequest("GET", "http://localhost:8080/api/config", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var config struct {
		CSRF string `json:"csrf"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &config) != nil || config.CSRF == "" {
		panic("test server did not provide a request token")
	}
	return config.CSRF
}
