package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"awarddesk/internal/storage"
)

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
