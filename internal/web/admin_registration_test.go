package web

import (
	"awarddesk/internal/registration"
	"strings"
	"testing"
)

func TestRegistrationImportRequiresAdminConsentAndKeepsSecretsPrivate(t *testing.T) {
	s, h := adminTestServer(t)
	svc, err := registration.New(s.Admin.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	s.Admin.Registrations = svc // Do not start workers: this test makes no provider requests.
	cookie := adminLogin(t, s, h)
	s.Admin.DB.SetSettings(map[string]string{"PROXY_LIST": "http://example.com:8080", "HYPER_API_KEY": "test-hyper", "ANYSOLVER_API_KEY": "test-any"})
	const credentials = "fixture@outlook.com:discarded-password:private-refresh-token-1234:9e5f94bc-e8a4-4e73-b8be-63364c29d753"
	body := `{"credentials":"` + credentials + `","acceptTerms":true}`
	if w := adminRequest(h, s, "POST", "/api/admin/registrations", body, nil, true); w.Code != 401 {
		t.Fatal("unauthenticated import accepted")
	}
	if w := adminRequest(h, s, "POST", "/api/admin/registrations", body, cookie, false); w.Code != 403 {
		t.Fatal("stale token accepted")
	}
	if w := adminRequest(h, s, "POST", "/api/admin/registrations", `{"credentials":"`+credentials+`"}`, cookie, true); w.Code != 400 {
		t.Fatal("terms confirmation missing")
	}
	if _, total, _ := svc.List(1, 25); total != 0 {
		t.Fatal("rejected import had side effects")
	}
	if w := adminRequest(h, s, "POST", "/api/admin/registrations", body, cookie, true); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	w := adminRequest(h, s, "GET", "/api/admin/registrations", "", cookie, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "fixture@outlook.com") || strings.Contains(w.Body.String(), "private-refresh") || strings.Contains(w.Body.String(), "discarded-password") {
		t.Fatal("registration history leaks credentials or hides status")
	}
	items, _, _ := svc.List(1, 25)
	id := items[0].ID
	w = adminRequest(h, s, "GET", "/api/admin/registrations?ids="+id, "", cookie, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), id) || strings.Contains(w.Body.String(), "private-refresh") {
		t.Fatal("batch progress boundary")
	}
	if w = adminRequest(h, s, "GET", "/api/admin/registrations?ids=invalid", "", cookie, true); w.Code != 400 {
		t.Fatal("invalid batch ID accepted")
	}
	if w = adminRequest(h, s, "GET", "/api/admin/registrations?ids="+id, "", nil, true); w.Code != 401 {
		t.Fatal("batch progress unauthenticated")
	}
	if _, err = s.Admin.DB.DB.Exec("UPDATE registrations SET data=json_set(data,'$.status','attention','$.error','verification needs inspection') WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	w = adminRequest(h, s, "GET", "/api/admin/overview", "", cookie, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "accountSetups") || !strings.Contains(w.Body.String(), "verification needs inspection") || strings.Contains(w.Body.String(), "private-refresh") {
		t.Fatal("unresolved signup missing from account overview or leaking OAuth")
	}
}
