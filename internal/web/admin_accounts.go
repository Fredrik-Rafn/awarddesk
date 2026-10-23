package web

import (
	"net/http"
	"net/mail"
	"strings"

	"awarddesk/internal/fleet"
	"awarddesk/internal/storage"
)

func (a *Admin) putAccount(w http.ResponseWriter, r *http.Request) {
	var v storage.Account
	if decodeAdmin(w, r, &v) != nil {
		fail(w, 400, "Invalid account")
		return
	}
	v.Email = strings.TrimSpace(v.Email)
	v.OutlookEmail = strings.TrimSpace(v.OutlookEmail)
	if _, e := mail.ParseAddress(v.Email); e != nil {
		fail(w, 400, "Enter a valid EVA email")
		return
	}
	if _, e := mail.ParseAddress(v.OutlookEmail); e != nil {
		fail(w, 400, "Enter a valid Outlook email")
		return
	}
	if len(v.Label) > 120 || len(v.Password) > 512 || len(v.RefreshToken) > 16384 || len(v.ClientID) > 200 {
		fail(w, 400, "Account field is too long")
		return
	}
	old := storage.Account{}
	rows, e := a.DB.Accounts()
	if e != nil {
		fail(w, 500, "Could not read accounts")
		return
	}
	for _, row := range rows {
		if row.ID == v.ID {
			old = row
		}
	}
	if v.ID != "" && old.ID == "" {
		fail(w, 404, "Account not found")
		return
	}
	if v.ID == "" {
		v.ID = storage.ID()
	}
	if old.ID != "" {
		if !strings.EqualFold(old.Email, v.Email) {
			fail(w, 400, "Create a new account to change its EVA email")
			return
		}
		if v.Password == "" {
			v.Password = old.Password
		}
		if v.RefreshToken == "" {
			v.RefreshToken = old.RefreshToken
		}
	}
	if v.Password == "" || v.RefreshToken == "" || v.ClientID == "" {
		fail(w, 400, "EVA password, Outlook client ID and refresh token are required")
		return
	}
	e = a.Fleet.Edit(v.ID, func() error {
		if old.RefreshToken != "" && (old.RefreshToken != v.RefreshToken || old.ClientID != v.ClientID || old.OutlookEmail != v.OutlookEmail) {
			if e := a.DB.DeleteState(storage.MailboxKey(old.OutlookEmail, old.ClientID)); e != nil {
				return e
			}
		}
		if old.Password != "" && old.Password != v.Password {
			if e := a.DB.DeleteState(storage.AccountKey(old.Email)); e != nil {
				return e
			}
		}
		if e := a.DB.PutAccount(v); e != nil {
			return e
		}
		return a.DB.Runtime("account:"+v.ID, fleet.Status{ID: v.ID, State: "idle"})
	})
	if e != nil {
		fail(w, 409, "Could not save account; account may be busy or email already exists")
		return
	}
	writeJSON(w, 200, map[string]bool{"saved": true})
}
func (a *Admin) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	rows, _ := a.DB.Accounts()
	for _, v := range rows {
		if v.ID == id {
			e := a.Fleet.Edit(id, func() error {
				if e := a.DB.DeleteState(storage.AccountKey(v.Email)); e != nil {
					return e
				}
				shared := false
				for _, other := range rows {
					if other.ID != id && strings.EqualFold(other.OutlookEmail, v.OutlookEmail) && other.ClientID == v.ClientID {
						shared = true
					}
				}
				if !shared {
					if e := a.DB.DeleteState(storage.MailboxKey(v.OutlookEmail, v.ClientID)); e != nil {
						return e
					}
				}
				return a.DB.DeleteAccount(id)
			})
			if e != nil {
				fail(w, 409, "Account is busy or could not be removed")
				return
			}
			w.WriteHeader(204)
			return
		}
	}
	fail(w, 404, "Account not found")
}
func (a *Admin) prepare(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID string `json:"id"`
	}
	if decodeAdmin(w, r, &b) != nil || b.ID == "" {
		fail(w, 400, "Account ID required")
		return
	}
	rows, _ := a.DB.Accounts()
	found := false
	for _, v := range rows {
		if v.ID == b.ID && v.Enabled {
			found = true
		}
	}
	if !found {
		fail(w, 404, "Enabled account not found")
		return
	}
	if e := a.Fleet.RequestPrepare(b.ID); e != nil {
		fail(w, 409, e.Error())
		return
	}
	writeJSON(w, 202, map[string]bool{"started": true})
}
func (a *Admin) invalidate(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID string `json:"id"`
	}
	if decodeAdmin(w, r, &b) != nil {
		fail(w, 400, "Account ID required")
		return
	}
	rows, _ := a.DB.Accounts()
	for _, v := range rows {
		if v.ID == b.ID {
			e := a.Fleet.Edit(v.ID, func() error { return a.DB.DeleteState(storage.AccountKey(v.Email)) })
			if e != nil {
				fail(w, 409, "Account is busy or session could not be removed")
				return
			}
			w.WriteHeader(204)
			return
		}
	}
	fail(w, 404, "Account not found")
}
