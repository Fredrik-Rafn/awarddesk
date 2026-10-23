package eva

import (
	"encoding/json"
	"time"
)

func (p *Provider) expiry(s cached) (time.Time, string) {
	age := p.config.SessionMaxAge
	if age <= 5*time.Minute {
		age = 3 * time.Hour
	}
	deadline := s.Created.Add(age)
	source := "configured maximum age (server lifetime unconfirmed)"
	for _, event := range s.HTTP.Cookies {
		for _, c := range event.Cookies {
			if c != nil && c.Name == "EVAID" && !c.Expires.IsZero() && c.Expires.Before(deadline) {
				deadline = c.Expires
				source = "EVAID cookie deadline"
			}
		}
	}
	deadline = deadline.Add(-5 * time.Minute)
	if !s.ExpiresAt.IsZero() && s.ExpiresAt.Before(deadline) {
		return s.ExpiresAt, s.ExpirySource
	}
	return deadline, source
}
func (p *Provider) sessionExpiry(s cached) time.Time { t, _ := p.expiry(s); return t }

type SessionInfo struct {
	Present      bool      `json:"present"`
	Ready        bool      `json:"ready"`
	Created      time.Time `json:"createdAt,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
	ExpirySource string    `json:"expirySource,omitempty"`
}

func (p *Provider) SessionInfo() SessionInfo {
	var s cached
	b, e := p.store().Load()
	if e != nil || json.Unmarshal(b, &s) != nil || s.Version != 1 || s.Created.IsZero() || s.Created.After(time.Now()) || s.Identity != p.identity() {
		return SessionInfo{}
	}
	expiry, source := p.expiry(s)
	return SessionInfo{Present: true, Ready: time.Now().Before(expiry), Created: s.Created, ExpiresAt: expiry, ExpirySource: source}
}
