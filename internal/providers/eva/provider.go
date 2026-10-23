package eva

import (
	"time"

	"awarddesk/internal/exchange"
	"awarddesk/internal/outlook"
	"awarddesk/internal/proxies"
	"awarddesk/internal/sessionstore"
)

const LoginURL = "https://eservice.evaair.com/flyeva/eva/ffp/login.aspx"
const ItineraryURL = "https://eservice.evaair.com/flyeva/eva/ffp/frequent-flyer.aspx"

type Config struct {
	Exchange                          *exchange.Service
	StateBackend                      sessionstore.Backend
	SessionMaxAge                     time.Duration
	Email, Password, HyperKey, AnyKey string
	ImpervaSolver                     string
	Pool                              *proxies.Pool
	Mailbox                           *outlook.Reader
	ResultCacheTTL                    time.Duration
}
type Provider struct {
	config        Config
	gate          chan struct{}
	warm          *warmSession
	awardAirports *awardAirportDirectory
	closed        bool
}

func New(c Config) *Provider {
	if c.Exchange == nil {
		c.Exchange = exchange.New(c.StateBackend)
	}
	return &Provider{config: c, gate: make(chan struct{}, 1)}
}
