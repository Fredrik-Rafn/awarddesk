package web

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEditableSecretsRequireAdminAndLegacySettingsStayStored(t *testing.T) {
	s, h := adminTestServer(t)
	cookie := adminLogin(t, s, h)
	s.Admin.DB.SetSettings(map[string]string{"ANA_PASSWORD": "old-ana-password", "BA_PASSWORD": "old-ba-password"})
	w := adminRequest(h, s, "GET", "/api/admin/settings", "", cookie, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "test-secret-key") || strings.Contains(w.Body.String(), "ANA_PASSWORD") || strings.Contains(w.Body.String(), "BA_PASSWORD") {
		t.Fatal("settings edit/hiding contract")
	}
	w = adminRequest(h, s, "PUT", "/api/admin/settings", `{"values":{"HYPER_API_KEY":""}}`, cookie, true)
	if w.Code != 200 || s.Admin.DB.Setting("HYPER_API_KEY") != "" || s.Admin.DB.Setting("ANA_PASSWORD") != "old-ana-password" || s.Admin.DB.Setting("BA_PASSWORD") != "old-ba-password" {
		t.Fatal("clearing one key changed hidden values")
	}
	account := `{"label":"Test","email":"test@example.com","password":"private-password","outlookEmail":"test@outlook.com","clientId":"client","refreshToken":"private-refresh","enabled":false}`
	if w = adminRequest(h, s, "PUT", "/api/admin/accounts", account, cookie, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	rows, _ := s.Admin.DB.Accounts()
	path := "/api/admin/accounts?id=" + rows[0].ID
	if w = adminRequest(h, s, "GET", path, "", nil, true); w.Code != 401 {
		t.Fatal("account secrets unauthenticated")
	}
	w = adminRequest(h, s, "GET", path, "", cookie, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "private-password") || !strings.Contains(w.Body.String(), "private-refresh") {
		t.Fatal("account editor cannot load saved secrets")
	}
	w = adminRequest(h, s, "PUT", "/api/admin/settings", `{"values":{"SEARCH_CONCURRENCY":"8"}}`, cookie, false)
	var e map[string]string
	json.Unmarshal(w.Body.Bytes(), &e)
	if w.Code != 403 || e["code"] != "request_token_invalid" || s.Admin.DB.Setting("SEARCH_CONCURRENCY") == "8" {
		t.Fatal("stale token performed action or missing recovery code")
	}
}
