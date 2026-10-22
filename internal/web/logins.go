package web

import (
	"net/http"

	"awarddesk/internal/login"
)

// loginRoutes keeps the same status/start/cancel contract for both airlines.
// EVA additionally checks its live fleet settings before starting a check.
func (s *Server) loginRoutes(mux *http.ServeMux) {
	s.loginRoute(mux, "ana", "ANA", s.ANALogin,
		"Configure ANA_MEMBER_NUMBER, ANA_PASSWORD and HYPER_API_KEY, then restart", nil)
	s.loginRoute(mux, "eva", "EVA", s.EVALogin,
		"Configure EVA accounts, Outlook OAuth, proxies and solver keys in Admin", func() bool {
			return s.Admin == nil || s.Admin.Fleet.Ready()
		})
}

func (s *Server) loginRoute(mux *http.ServeMux, id, name string, service *login.Service, configurationMessage string, ready func() bool) {
	path := "/api/logins/" + id
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			fail(w, http.StatusServiceUnavailable, name+" login is not configured")
			return
		}
		writeJSON(w, http.StatusOK, service.Status())
	})
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		if service == nil || !service.Ready() || ready != nil && !ready() {
			fail(w, http.StatusServiceUnavailable, configurationMessage)
			return
		}
		if err := service.Start(); err != nil {
			fail(w, http.StatusTooManyRequests, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, service.Status())
	})
	mux.HandleFunc("DELETE "+path, func(w http.ResponseWriter, r *http.Request) {
		if service != nil {
			service.Cancel()
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
