package exchange

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/sessionstore"
)

const endpoint = "https://open.er-api.com/v6/latest/USD"
const source = "https://www.exchangerate-api.com"
const maxBody = 128 << 10

var decimal = regexp.MustCompile(`^(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]{1,2})?$`)
var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

type rates struct {
	Result string                 `json:"result"`
	Base   string                 `json:"base_code"`
	Last   int64                  `json:"time_last_update_unix"`
	Next   int64                  `json:"time_next_update_unix"`
	Rates  map[string]json.Number `json:"rates"`
}

type Service struct {
	store     sessionstore.Store
	client    *http.Client
	url       string
	now       func() time.Time
	gate      chan struct{}
	cached    *rates
	loaded    bool
	retryAt   time.Time
	lastError error
}

func New(backend sessionstore.Backend) *Service {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Service{
		store:  sessionstore.Store{Path: ".state/exchange/usd.json", Backend: backend},
		client: &http.Client{Transport: transport, Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		url:    endpoint, now: time.Now, gate: make(chan struct{}, 1),
	}
}

func number(s string) (*big.Rat, bool) {
	if len(s) > 64 || !decimal.MatchString(s) {
		return nil, false
	}
	v, ok := new(big.Rat).SetString(s)
	if !ok || v.Sign() < 0 || v.Cmp(new(big.Rat).SetInt64(1_000_000_000_000_000)) > 0 {
		return nil, false
	}
	return v, true
}

func decode(raw []byte, now time.Time) (*rates, error) {
	var r rates
	if len(raw) > maxBody || json.Unmarshal(raw, &r) != nil || r.Result != "success" || r.Base != "USD" || r.Last <= 0 || r.Next <= r.Last {
		return nil, errors.New("invalid USD exchange response")
	}
	asOf, next := time.Unix(r.Last, 0), time.Unix(r.Next, 0)
	if asOf.After(now) || now.Sub(asOf) > 48*time.Hour || !next.After(now) || next.Sub(asOf) > 48*time.Hour {
		return nil, errors.New("USD exchange rates are stale or have invalid dates")
	}
	usd, ok := number(r.Rates["USD"].String())
	if !ok || usd.Cmp(big.NewRat(1, 1)) != 0 {
		return nil, errors.New("invalid USD base rate")
	}
	for code, raw := range r.Rates {
		v, ok := number(raw.String())
		if !currencyCode.MatchString(code) || !ok || v.Sign() <= 0 || v.Cmp(big.NewRat(1, 1_000_000_000_000)) < 0 || v.Cmp(new(big.Rat).SetInt64(1_000_000_000_000)) > 0 {
			return nil, errors.New("invalid exchange rate")
		}
	}
	return &r, nil
}

func roundUSD(v *big.Rat) string {
	cents := new(big.Rat).Mul(v, big.NewRat(100, 1))
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(cents.Num(), cents.Denom(), remainder)
	if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(cents.Denom()) >= 0 {
		whole.Add(whole, big.NewInt(1))
	}
	return new(big.Rat).SetFrac(whole, big.NewInt(100)).FloatString(2)
}

func (s *Service) USD(ctx context.Context, amount award.Money) (*award.USDConversion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v, ok := number(amount.Amount)
	if !ok || !currencyCode.MatchString(amount.Currency) {
		return nil, errors.New("invalid original currency amount")
	}
	if amount.Currency == "USD" {
		return &award.USDConversion{Money: award.Money{Amount: roundUSD(v), Currency: "USD"}, Rate: "1", Source: "native"}, nil
	}
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := s.now()
	if !s.loaded {
		s.loaded = true
		if raw, err := s.store.Load(); err == nil {
			s.cached, _ = decode(raw, now)
		}
	}
	if s.cached == nil || !time.Unix(s.cached.Next, 0).After(now) || time.Unix(s.cached.Last, 0).After(now) || now.Sub(time.Unix(s.cached.Last, 0)) > 48*time.Hour {
		if now.Before(s.retryAt) {
			return nil, s.lastError
		}
		if err := s.refresh(ctx); err != nil {
			if ctx.Err() == nil {
				s.retryAt, s.lastError = s.now().Add(30*time.Second), err
			}
			return nil, err
		}
		s.retryAt, s.lastError = time.Time{}, nil
	}
	rate, exists := s.cached.Rates[amount.Currency]
	if !exists {
		return nil, errors.New("exchange rate unavailable for original currency")
	}
	units, _ := number(rate.String())
	dollars := new(big.Rat).Quo(v, units)
	return &award.USDConversion{Money: award.Money{Amount: roundUSD(dollars), Currency: "USD"}, Rate: rate.String(), AsOf: time.Unix(s.cached.Last, 0).UTC(), Source: source}, nil
}

func (s *Service) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	resp.Body.Close()
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode != http.StatusOK {
		return errors.New("USD exchange request failed")
	}
	r, err := decode(raw, s.now())
	if err != nil {
		return err
	}
	if err = s.store.Save(raw); err != nil {
		return err
	}
	s.cached = r
	return nil
}
