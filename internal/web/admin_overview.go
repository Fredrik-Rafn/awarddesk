package web

import (
	"net/http"
	"strings"
	"time"

	"awarddesk/internal/registration"
	"awarddesk/internal/storage"
)

// Build the dashboard summary without returning account passwords or tokens.
func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	a := s.Admin
	accounts, e := a.DB.Accounts()
	if e != nil {
		fail(w, 500, "Could not read accounts")
		return
	}
	type accountView struct {
		storage.Account
		Configured bool `json:"configured"`
	}
	accountViews := make([]accountView, 0, len(accounts))
	for _, account := range accounts {
		complete := account.Email != "" && account.Password != "" && account.OutlookEmail != "" && account.ClientID != "" && account.RefreshToken != ""
		account.Password = ""
		account.RefreshToken = ""
		accountViews = append(accountViews, accountView{account, complete})
	}
	statuses, e := a.Fleet.Statuses()
	if e != nil {
		fail(w, 500, "Could not read sessions")
		return
	}
	flights, e := a.DB.CachedFlights()
	if e != nil {
		fail(w, 500, "Could not read saved flights")
		return
	}
	history, e := a.DB.JobSummaries()
	if e != nil {
		fail(w, 500, "Could not read search history")
		return
	}
	proxyCount := 0
	for _, line := range strings.Split(a.DB.Setting("PROXY_LIST"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			proxyCount++
		}
	}
	setups := []registration.Summary{}
	if a.Registrations != nil {
		setups, e = a.Registrations.AccountSetups()
		if e != nil {
			fail(w, 500, "Could not read account setup status")
			return
		}
	}
	writeJSON(w, 200, map[string]any{"proxyCount": proxyCount, "accounts": accountViews, "sessions": statuses, "accountSetups": setups, "flights": flights, "jobs": history, "now": time.Now().UTC(), "ready": a.Fleet.Ready(), "concurrency": a.DB.Setting("SEARCH_CONCURRENCY"), "historyPermanent": true})
}
