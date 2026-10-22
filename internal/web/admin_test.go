package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"awarddesk/internal/fleet"
	"awarddesk/internal/jobs"
	"awarddesk/internal/storage"
)

func adminTestServer(t *testing.T) (*Server, http.Handler) {
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
	s, e := New(j, true, true)
	if e != nil {
		t.Fatal(e)
	}
	s.Admin = &Admin{DB: db, Fleet: f}
	t.Cleanup(func() { j.Close(); f.Close(); db.Close() })
	return s, s.Handler()
}
func adminRequest(h http.Handler, s *Server, method, path, body string, cookie *http.Cookie, csrf bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost:8080"+path, bytes.NewBufferString(body))
	r.Host = "localhost:8080"
	r.Header.Set("Origin", "http://localhost:8080")
	r.Header.Set("Content-Type", "application/json")
	if csrf {
		r.Header.Set("X-CSRF-Token", s.csrf)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func adminLogin(t *testing.T, s *Server, h http.Handler) *http.Cookie {
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
func TestAdminAuthSecretsAndCSRF(t *testing.T) {
	s, h := adminTestServer(t)
	if w := adminRequest(h, s, "GET", "/api/admin/settings", "", nil, true); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := adminRequest(h, s, "POST", "/api/admin/session", `{"password":"wrong"}`, nil, true); w.Code != 401 {
		t.Fatal(w.Code)
	}
	c := adminLogin(t, s, h)
	w := adminRequest(h, s, "GET", "/api/admin/settings", "", c, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "test-secret-key") || strings.Contains(w.Body.String(), "_ADMIN_HASH") {
		t.Fatal("authenticated settings must show editable values, never internal authentication hashes")
	}
	w = adminRequest(h, s, "PUT", "/api/admin/settings", `{"values":{"HYPER_API_KEY":"replacement"}}`, c, false)
	if w.Code != 403 {
		t.Fatal("CSRF was not required")
	}
	w = adminRequest(h, s, "GET", "/api/config", "", nil, true)
	if strings.Contains(w.Body.String(), "test-secret-key") {
		t.Fatal("public config exposed secret")
	}
	_ = adminRequest(h, s, "DELETE", "/api/admin/session", "", c, true)
	if w = adminRequest(h, s, "GET", "/api/admin/overview", "", c, true); w.Code != 401 {
		t.Fatal("logout token still valid")
	}
}
func TestAdminAccountCRUDAndSettingsValidation(t *testing.T) {
	s, h := adminTestServer(t)
	c := adminLogin(t, s, h)
	body := `{"label":"Example","email":"eva@example.com","password":"test-password","outlookEmail":"example@outlook.com","clientId":"test-client","refreshToken":"test-refresh","enabled":false}`
	w := adminRequest(h, s, "PUT", "/api/admin/accounts", body, c, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = adminRequest(h, s, "GET", "/api/admin/overview", "", c, true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "test-password") || strings.Contains(w.Body.String(), "test-refresh") {
		t.Fatal("overview exposed account secret")
	}
	var view struct {
		Accounts []storage.Account `json:"accounts"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &view); e != nil || len(view.Accounts) != 1 {
		t.Fatal("missing account")
	}
	w = adminRequest(h, s, "PUT", "/api/admin/settings", `{"values":{"SEARCH_CONCURRENCY":"999","EVA_RESULT_CACHE_TTL":"30m"}}`, c, true)
	if w.Code != 400 {
		t.Fatal("bad concurrency accepted")
	}
	w = adminRequest(h, s, "PUT", "/api/admin/settings", `{"values":{"SEARCH_CONCURRENCY":"8","EVA_RESULT_CACHE_TTL":"30m"}}`, c, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = adminRequest(h, s, "DELETE", "/api/admin/accounts?id="+view.Accounts[0].ID, "", c, true)
	if w.Code != 204 {
		t.Fatal(w.Body.String())
	}
}
