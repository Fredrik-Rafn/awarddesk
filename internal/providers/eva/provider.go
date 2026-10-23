// Package eva runs EVA login, registration and Miles & Cash searches using HTTP.
package eva

import (
	"time"

	"awarddesk/internal/outlook"
	"awarddesk/internal/proxies"
	"awarddesk/internal/sessionstore"
)

const LoginURL = "https://eservice.evaair.com/flyeva/eva/ffp/login.aspx"
const ItineraryURL = "https://eservice.evaair.com/flyeva/eva/ffp/frequent-flyer.aspx"

// Config supplies the account and shared services. A Provider owns one account;
// the fleet chooses which idle account handles a search.
type Config struct {
	StateBackend                      sessionstore.Backend
	SessionMaxAge                     time.Duration
	Email, Password, HyperKey, AnyKey string
	ImpervaSolver                     string
	Pool                              *proxies.Pool
	Mailbox                           *outlook.Reader
	ResultCacheTTL                    time.Duration
}
type Provider struct {
	config Config
	gate   chan struct{}
	warm   *warmSession // Protected by gate and the on-disk account lock.
	closed bool
}

func New(c Config) *Provider { return &Provider{config: c, gate: make(chan struct{}, 1)} }
