package web

import (
	"encoding/json"
	"testing"
)

func TestAdminCacheSwitchAuthConfirmationAndHistory(t *testing.T) {
	s, h := adminTestServer(t)
	cookie := adminLogin(t, s, h)
	if e := s.Admin.DB.WriteState(".state/eva-results/flight.json", []byte(`{"result":{"complete":true}}`)); e != nil {
		t.Fatal(e)
	}
	if e := s.Admin.DB.SaveJob("history", json.RawMessage(`{"id":"history","status":"completed"}`)); e != nil {
		t.Fatal(e)
	}
	w := adminRequest(h, s, "PUT", "/api/admin/cache-policy", `{"enabled":false,"confirmClear":true}`, nil, true)
	if w.Code != 401 {
		t.Fatal("toggle is unauthenticated")
	}
	w = adminRequest(h, s, "PUT", "/api/admin/cache-policy", `{"enabled":false}`, cookie, true)
	if w.Code != 409 {
		t.Fatal("toggle skipped confirmation")
	}
	w = adminRequest(h, s, "PUT", "/api/admin/cache-policy", `{"enabled":false,"confirmClear":true}`, cookie, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var p struct{ Enabled bool }
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if p.Enabled {
		t.Fatal("disable did not apply")
	}
	w = adminRequest(h, s, "PUT", "/api/admin/settings", `{"values":{"FLIGHT_CACHE_ENABLED":"false"}}`, cookie, true)
	if w.Code != 409 {
		t.Fatal("generic setting bypasses warning")
	}
	w = adminRequest(h, s, "PUT", "/api/admin/settings", `{"values":{"HISTORY_RETENTION_DAYS":"1"}}`, cookie, true)
	if w.Code != 400 {
		t.Fatal("history retention can still be configured")
	}
	w = adminRequest(h, s, "GET", "/api/admin/search?id=history", "", cookie, true)
	if w.Code != 200 {
		t.Fatal("clearing cache deleted search history")
	}
	w = adminRequest(h, s, "PUT", "/api/admin/cache-policy", `{"enabled":true}`, cookie, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = adminRequest(h, s, "PUT", "/api/admin/cache-policy", `{}`, cookie, true)
	if w.Code != 400 {
		t.Fatal("missing bool accepted")
	}
}
