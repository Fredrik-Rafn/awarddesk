package eva

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/session"
	"awarddesk/internal/sessionstore"
)

// A saved member session keeps cookies, browser identity and its original proxy
// together. Restoring cookies on a different IP can invalidate the login.
type cached struct {
	Version         int
	Account         string
	Identity        string
	Created         time.Time
	ExpiresAt       time.Time
	ExpirySource    string
	Proxy, Template string
	HTTP            session.State
}

func (p *Provider) identity() string {
	h := sha256.Sum256([]byte(p.config.Email + "\x00" + p.config.Password + "\x00" + session.UserAgent))
	return hex.EncodeToString(h[:])
}
func (p *Provider) store() sessionstore.Store {
	h := sha256.Sum256([]byte(strings.ToLower(p.config.Email)))
	return sessionstore.Store{Backend: p.config.StateBackend, Path: filepath.Join(".state", "eva", hex.EncodeToString(h[:])+".json")}
}

// Save verified member state before and after using it for a search. The search
// error takes priority if saving the refreshed cookies also fails.
func (p *Provider) finish(store sessionstore.Store, c *session.Client, saved cached, use func(*session.Client, string) error) error {
	if err := p.save(store, c, saved); err != nil {
		return err
	}
	if use == nil {
		return nil
	}
	err := use(c, saved.Proxy)
	saveErr := p.save(store, c, saved)
	if err != nil {
		return err
	}
	return saveErr
}
func (p *Provider) save(store sessionstore.Store, c *session.Client, saved cached) error {
	state, err := c.Snapshot()
	if err != nil {
		return award.Failure("session_store", "Could not snapshot EVA session")
	}
	saved.HTTP = state
	saved.ExpiresAt, saved.ExpirySource = p.expiry(saved)
	saved.Account = p.config.Email
	data, err := json.Marshal(saved)
	if err == nil {
		err = store.Save(data)
	}
	if err != nil {
		return award.Failure("session_store", "Could not save EVA session")
	}
	if p.warm != nil && p.warm.client == c {
		p.warm.diskKey = sessionSnapshotKey(data)
		p.warm.usedAt = time.Now()
	}
	return nil
}
