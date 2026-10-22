package web

import (
	"net/http"
	"strconv"
	"strings"
)

func (a *Admin) registrations(w http.ResponseWriter, r *http.Request) {
	if a.Registrations == nil {
		fail(w, 503, "Registration service is unavailable")
		return
	}
	switch r.Method {
	case "GET":
		if raw := r.URL.Query().Get("ids"); raw != "" {
			ids := strings.Split(raw, ",")
			if len(ids) > 500 {
				fail(w, 400, "Batch is too large")
				return
			}
			for _, id := range ids {
				if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
					fail(w, 400, "Invalid batch ID")
					return
				}
			}
			items, e := a.Registrations.Current(ids)
			if e != nil {
				fail(w, 500, "Could not read account creation progress")
				return
			}
			writeJSON(w, 200, map[string]any{"items": items})
			return
		}
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
func (a *Admin) retryAllRegistrations(w http.ResponseWriter, r *http.Request) {
	if a.Registrations == nil {
		fail(w, 503, "Registration service is unavailable")
		return
	}
	ids, skipped, err := a.Registrations.RetryAll()
	if err != nil {
		fail(w, 500, "Could not queue account setups")
		return
	}
	writeJSON(w, 202, map[string]any{"ids": ids, "queued": len(ids), "skipped": skipped})
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

func (a *Admin) restartRegistration(w http.ResponseWriter, r *http.Request) {
	if a.Registrations == nil {
		fail(w, 503, "Registration service is unavailable")
		return
	}
	var body struct {
		ID      string `json:"id"`
		Confirm bool   `json:"confirmRestart"`
	}
	if decodeAdmin(w, r, &body) != nil || !body.Confirm {
		fail(w, 400, "Confirm that the mailbox and previous registration have been inspected before restarting signup")
		return
	}
	if err := a.Registrations.Restart(body.ID); err != nil {
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 202, map[string]bool{"queued": true})
}
