package ana

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/session"
)

// maxSessionAge is a conservative local reuse cap, not ANA's advertised expiry.
const maxSessionAge = 24 * time.Hour

type savedSession struct {
	Version       int
	Account       string
	Identity      string
	CreatedAt     time.Time
	Proxy         string
	ProxyTemplate string
	Token         string
	ClientID      string
	ClientKey     string
	Correlation   string
	HTTP          session.State
}

func (p *Provider) cacheIdentity() string {
	b, _ := json.Marshal([]any{p.config.MemberNumber, p.config.Password, p.config.Proxy, p.config.Pool != nil, session.UserAgent, session.Language, "Chrome_152-v1"})
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:])
}
func (p *Provider) AccountID() string {
	hash := sha256.Sum256([]byte(p.config.MemberNumber))
	return hex.EncodeToString(hash[:])
}
func (p *Provider) save(s *LoginSession) ([]byte, error) {
	httpState, err := s.client.Snapshot()
	if err != nil {
		return nil, award.Failure("session_store", "Could not snapshot ANA session")
	}
	return json.Marshal(savedSession{Version: 1, Account: p.AccountID(), Identity: p.cacheIdentity(), CreatedAt: s.createdAt, Proxy: s.proxy, ProxyTemplate: s.proxyTemplate, Token: s.token, ClientID: s.clientID, ClientKey: s.clientKey, Correlation: s.correlation, HTTP: httpState})
}

var errExpired = errors.New("saved session expired")

func (p *Provider) restore(ctx context.Context, data []byte) (*LoginSession, error) {
	var saved savedSession
	if json.Unmarshal(data, &saved) != nil || saved.Version != 1 || saved.Token == "" || saved.ClientID == "" || saved.ClientKey == "" {
		return nil, award.Failure("session_store", "Saved ANA session is invalid")
	}
	if saved.Account != p.AccountID() || saved.Identity != p.cacheIdentity() || saved.CreatedAt.IsZero() || time.Since(saved.CreatedAt) > maxSessionAge || saved.CreatedAt.After(time.Now().Add(time.Minute)) {
		return nil, errExpired
	}
	var release func(bool)
	if p.config.Pool != nil {
		lease, err := p.config.Pool.Resume(ctx, saved.ProxyTemplate, saved.Proxy)
		if err != nil {
			return nil, award.Failure("proxy_pool", "Saved ANA session proxy is unavailable; session was not moved to another proxy")
		}
		release = lease.Release
	} else if saved.Proxy != p.config.Proxy {
		return nil, errExpired
	}
	c, err := session.New(saved.Proxy, []string{"www.ana.co.jp", "space.ana.co.jp", "api.ipify.org"})
	if err == nil {
		err = c.Restore(saved.HTTP)
	}
	if err != nil {
		if c != nil {
			c.Close()
		}
		if release != nil {
			release(false)
		}
		return nil, award.Failure("session_store", "Saved ANA browser session could not be restored")
	}
	return &LoginSession{client: c, token: saved.Token, clientID: saved.ClientID, clientKey: saved.ClientKey, correlation: saved.Correlation, createdAt: saved.CreatedAt, proxy: saved.Proxy, proxyTemplate: saved.ProxyTemplate, release: release}, nil
}
func (p *Provider) verify(ctx context.Context, s *LoginSession, report award.Reporter) error {
	if s.proxy != "" {
		verifier, ok := p.config.Challenge.(interface {
			VerifyConnection(context.Context, *session.Client) error
		})
		if !ok {
			return award.Failure("proxy_verification", "Cannot verify saved ANA proxy identity")
		}
		if err := verifier.VerifyConnection(ctx, s.client); err != nil {
			return err
		}
	}
	f := loginFlow{client: s.client, config: p.config, report: report, clientID: s.clientID, clientKey: s.clientKey, token: s.token, correlation: s.correlation, stage: "session verification"}
	f.progress("Validating the saved ANA session")
	err := f.verifyMember(ctx)
	var ae *award.Error
	if errors.As(err, &ae) && (ae.HTTPStatus == 401 || ae.Code == "login_unverified") {
		return errExpired
	}
	if err != nil {
		return err
	}
	s.correlation = f.correlation
	return nil
}
