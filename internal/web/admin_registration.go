package web

import (
	"net/http"
	"strconv"
)

func (a *Admin) registrations(w http.ResponseWriter, r *http.Request) {
	if a.Registrations == nil {
		fail(w, 503, "Registration service is unavailable")
		return
	}
	switch r.Method {
	case "GET":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		if page > 1000000 {
			fail(w, 400, "Page is too large")
			return
		}
		items, total, e := a.Registrations.List(page, 25)
		if e != nil {
			fail(w, 500, "Could not read registrations")
			return
		}
		writeJSON(w, 200, map[string]any{"items": items, "total": total, "page": page, "limit": 25})
	case "POST":
		var body struct {
			Credentials string `json:"credentials"`
			AcceptTerms bool   `json:"acceptTerms"`
		}
		if decodeAdmin(w, r, &body) != nil {
			fail(w, 400, "Invalid registration batch")
			return
		}
		if !body.AcceptTerms {
			fail(w, 400, "Confirm EVA membership terms and privacy policy before creating accounts")
			return
		}
		if a.DB.Setting("PROXY_LIST") == "" || a.DB.Setting("HYPER_API_KEY") == "" || a.DB.Setting("ANYSOLVER_API_KEY") == "" {
			fail(w, 400, "Configure proxies and both solver keys first")
			return
		}
		result, e := a.Registrations.Import(body.Credentials)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		writeJSON(w, 202, result)
	default:
		fail(w, 405, "Method not allowed")
	}
}
func (a *Admin) retryRegistration(w http.ResponseWriter, r *http.Request) {
	if a.Registrations == nil {
		fail(w, 503, "Registration service is unavailable")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if decodeAdmin(w, r, &body) != nil {
		fail(w, 400, "Invalid registration ID")
		return
	}
	if e := a.Registrations.Retry(body.ID); e != nil {
		fail(w, 409, e.Error())
		return
	}
	writeJSON(w, 202, map[string]bool{"queued": true})
}
