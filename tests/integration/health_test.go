package integration

import (
	"net/http/httptest"
	"testing"
)

func TestRailwayHealthDoesNotRequireDemoCredentials(t *testing.T) {
	s, _ := adminTestServer(t)
	if err := s.ConfigureDemo(demoAddress, demoPassword); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	for _, method := range []string{"GET", "HEAD"} {
		r := httptest.NewRequest(method, "http://healthcheck.railway.app/healthz", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || (method == "HEAD" && w.Body.Len() != 0) || (method == "GET" && w.Body.String() != "ok\n") {
			t.Fatal("Railway health probe failed")
		}
	}
	for _, path := range []string{"/api/config", "/api/admin/settings", "/healthz/"} {
		r := httptest.NewRequest("GET", "http://healthcheck.railway.app"+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("health host bypassed application access checks")
		}
	}
	r := httptest.NewRequest("POST", "http://healthcheck.railway.app/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("health bypass accepted a mutation")
	}
}
