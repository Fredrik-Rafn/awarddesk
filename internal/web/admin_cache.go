package web

import (
	"awarddesk/internal/storage"
	"errors"
	"net/http"
)

func (a *Admin) cachePolicy(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Enabled      *bool `json:"enabled"`
		ConfirmClear bool  `json:"confirmClear"`
	}
	if decodeAdmin(w, r, &b) != nil || b.Enabled == nil {
		fail(w, 400, "Choose whether flight caching is enabled")
		return
	}
	if e := a.DB.SetCachePolicy(*b.Enabled, b.ConfirmClear); e != nil {
		if errors.Is(e, storage.ErrCacheConfirmation) {
			fail(w, 409, e.Error())
			return
		}
		fail(w, 500, "Could not update flight caching")
		return
	}
	policy, e := a.DB.CachePolicy()
	if e != nil {
		fail(w, 500, "Could not read flight caching")
		return
	}
	writeJSON(w, 200, policy)
}
