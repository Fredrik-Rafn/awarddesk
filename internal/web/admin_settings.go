package web

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/storage"
)

func secretSetting(k string) bool {
	return strings.Contains(k, "KEY") || strings.Contains(k, "PASSWORD") || strings.Contains(k, "TOKEN")
}
func (a *Admin) settings(w http.ResponseWriter) {
	m, e := a.DB.RuntimeSettings()
	if e != nil {
		fail(w, 500, "Could not read settings")
		return
	}
	policy, e := a.DB.CachePolicy()
	if e != nil {
		fail(w, 500, "Could not read cache policy")
		return
	}
	m["FLIGHT_CACHE_ENABLED"] = strconv.FormatBool(policy.Enabled)
	type setting struct {
		Value      string `json:"value"`
		Secret     bool   `json:"secret"`
		Configured bool   `json:"configured"`
	}
	out := map[string]setting{}
	for k, v := range m {
		if !storage.EditableSetting(k) {
			continue
		}
		row := setting{Value: v, Secret: secretSetting(k), Configured: v != ""}
		out[k] = row
	}
	writeJSON(w, 200, out)
}

var settingName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,79}$`)

func validateSetting(k, v string) error {
	if !settingName.MatchString(k) || k == "PROXY_LIST" {
		return errors.New("Invalid setting name")
	}
	switch k {
	case "EVA_RESULT_CACHE_TTL":
		d, e := time.ParseDuration(v)
		if e != nil || d < 0 || d > 24*time.Hour {
			return errors.New("Cache duration must be between 0s and 24h")
		}
	case "EVA_SESSION_MAX_AGE":
		d, e := time.ParseDuration(v)
		if e != nil || d < 10*time.Minute || d > 24*time.Hour {
			return errors.New("Session maximum must be between 10m and 24h")
		}
	case "HISTORY_RETENTION_DAYS":
		return errors.New("Search history is permanent; retention is no longer configurable")
	case "SEARCH_CONCURRENCY":
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > award.MaxConcurrency {
			return errors.New("Concurrency must be 1 to 512")
		}
	case "EVA_IMPERVA_SOLVER":
		if v != "auto" && v != "hyper" && v != "anysolver" {
			return errors.New("Solver must be auto, hyper or anysolver")
		}
	case "EVA_BACKGROUND_ENABLED", "FLIGHT_CACHE_ENABLED":
		if v != "true" && v != "false" {
			return errors.New("Background settings must be true or false")
		}
	case "AWARDDESK_DB":
		return errors.New("Database path is a bootstrap environment option, not a runtime setting")
	case "EVA_EMAIL", "EVA_PASSWORD", "OUTLOOK_EMAIL", "OUTLOOK_CLIENT_ID", "OUTLOOK_REFRESH_TOKEN", "AWARDDESK_PROXY_FILE":
		return errors.New("Manage EVA credentials in Accounts and the proxy list in Proxies")
	}
	if !storage.EditableSetting(k) {
		return errors.New("Unknown setting")
	}
	return nil
}
func (a *Admin) putSettings(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Values            map[string]string `json:"values"`
		Clear             []string          `json:"clear"`
		ConfirmCacheClear bool              `json:"confirmCacheClear"`
	}
	if decodeAdmin(w, r, &b) != nil {
		fail(w, 400, "Invalid settings")
		return
	}
	if b.Values == nil {
		b.Values = map[string]string{}
	}
	for _, k := range b.Clear {
		if !secretSetting(k) {
			fail(w, 400, "Only secret fields support explicit clearing")
			return
		}
		b.Values[k] = ""
	}
	for k, v := range b.Values {
		if e := validateSetting(k, v); e != nil {
			fail(w, 400, e.Error())
			return
		}
	}
	if err := a.DB.ApplySettings(b.Values, b.ConfirmCacheClear); err != nil {
		if errors.Is(err, storage.ErrCacheConfirmation) {
			fail(w, 409, err.Error())
			return
		}
		fail(w, 500, "Could not save settings")
		return
	}
	if value, ok := b.Values["SEARCH_CONCURRENCY"]; ok {
		limit := award.ConcurrencyLimit(value)
		if a.Searches != nil {
			a.Searches.EnsureWorkers(limit)
		}
		a.Fleet.RefreshPreparationWorkers()
		if a.Registrations != nil {
			a.Registrations.RefreshWorkers()
		}
	}
	writeJSON(w, 200, map[string]bool{"saved": true})
}
