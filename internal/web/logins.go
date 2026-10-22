package web

import "net/http"

// loginRoutes exposes the bounded EVA preparation check. Fleet maintenance owns
// automatic refresh and per-account recovery.
func (s *Server) loginRoutes(mux *http.ServeMux) {
	service := s.EVALogin
	mux.HandleFunc("GET /api/logins/eva", func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			fail(w, http.StatusServiceUnavailable, "EVA login is not configured")
			return
		}
		writeJSON(w, http.StatusOK, service.Status())
	})
	mux.HandleFunc("POST /api/logins/eva", func(w http.ResponseWriter, r *http.Request) {
		if service == nil || !service.Ready() || s.Admin != nil && !s.Admin.Fleet.Ready() {
			fail(w, http.StatusServiceUnavailable, "Configure EVA accounts, Outlook OAuth, proxies and solver keys in Admin")
			return
		}
		if err := service.Start(); err != nil {
			fail(w, http.StatusTooManyRequests, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, service.Status())
	})
	mux.HandleFunc("DELETE /api/logins/eva", func(w http.ResponseWriter, r *http.Request) {
		if service != nil {
			service.Cancel()
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
