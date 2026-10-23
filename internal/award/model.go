package award

import (
	"context"
	"errors"
	"regexp"
	"time"
)

type Cabin string

const (
	Economy        Cabin = "economy"
	PremiumEconomy Cabin = "premium_economy"
	Business       Cabin = "business"
	First          Cabin = "first"
)

func (c Cabin) Valid() bool {
	return c == Economy || c == PremiumEconomy || c == Business || c == First
}

type Query struct {
	Provider    string `json:"provider"`
	Origin      string `json:"origin"`
	Destination string `json:"destination"`
	Date        string `json:"date"`
	Cabin       Cabin  `json:"cabin"`
	Adults      int    `json:"adults"`

	Mode    string `json:"mode,omitempty"`
	Refresh bool   `json:"refresh,omitempty"`
}

var iata = regexp.MustCompile(`^[A-Z]{3}$`)

func (q Query) Validate(now time.Time) error {
	if q.Provider == "" || !iata.MatchString(q.Origin) || !iata.MatchString(q.Destination) || q.Origin == q.Destination {
		return errors.New("choose a provider and two different three-letter airports")
	}
	d, e := time.Parse("2006-01-02", q.Date)
	if e != nil {
		return errors.New("date must use YYYY-MM-DD")
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if d.Before(today) || d.After(today.AddDate(1, 0, 0)) {
		return errors.New("choose a date within the next year")
	}
	if !q.Cabin.Valid() {
		return errors.New("unsupported cabin")
	}
	if q.Adults < 1 || q.Adults > 9 {
		return errors.New("adults must be between 1 and 9")
	}
	if q.Mode != "" && q.Mode != "award" {
		return errors.New("unsupported search mode")
	}
	if q.Mode == "award" && q.Adults > 6 {
		return errors.New("EVA award availability supports at most 6 adults")
	}
	if q.Mode == "award" && d.After(today.AddDate(0, 0, 360)) {
		return errors.New("EVA award searches must be within the next 360 days")
	}
	return nil
}

type Money struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type Fare struct {
	Name     string         `json:"name"`
	Cabin    Cabin          `json:"cabin"`
	Points   int            `json:"points"`
	Program  string         `json:"program"`
	Taxes    Money          `json:"taxes"`
	TaxesUSD *USDConversion `json:"taxesUsd,omitempty"`

	TaxEstimate *TaxEstimate `json:"taxEstimate,omitempty"`
	Seats       *int         `json:"seats,omitempty"`
	Estimated   bool         `json:"estimated"`

	AvailableFor      int        `json:"availableFor,omitempty"`
	CashPrice         *Money     `json:"cashPrice,omitempty"`
	PricingType       string     `json:"pricingType,omitempty"`
	MilesCash         *MilesCash `json:"milesCash,omitempty"`
	QuoteID           string     `json:"-"`
	MilesCashEligible *bool      `json:"milesCashEligible,omitempty"`
}

type TaxEstimate struct {
	Total      Money          `json:"total"`
	Components []TaxComponent `json:"components,omitempty"`
	Source     string         `json:"source"`
	QuotedAt   time.Time      `json:"quotedAt"`
	USD        *USDConversion `json:"usd,omitempty"`
}

type USDConversion struct {
	Money
	Rate   string    `json:"rate"`
	AsOf   time.Time `json:"asOf"`
	Source string    `json:"source"`
}

type TaxComponent struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Amount Money  `json:"amount"`
}

type MilesCash struct {
	Miles         int   `json:"miles"`
	RemainingCash Money `json:"remainingCash"`
	Selectable    bool  `json:"selectable"`
}

type Segment struct {
	Cabin            Cabin   `json:"cabin,omitempty"`
	FlightNumber     string  `json:"flightNumber"`
	Airline          string  `json:"airline"`
	OperatingAirline string  `json:"operatingAirline,omitempty"`
	Origin           string  `json:"origin"`
	Destination      string  `json:"destination"`
	DepartureLocal   string  `json:"departureLocal"`
	ArrivalLocal     string  `json:"arrivalLocal"`
	Aircraft         *string `json:"aircraft"`
}
type Itinerary struct {
	ID       string    `json:"id"`
	Segments []Segment `json:"segments"`
	Fares    []Fare    `json:"fares"`
	Summary  string    `json:"summary"`
}

type Result struct {
	Query          Query       `json:"query"`
	FetchedAt      time.Time   `json:"fetchedAt"`
	Itineraries    []Itinerary `json:"itineraries"`
	Warnings       []string    `json:"warnings"`
	Complete       bool        `json:"complete"`
	Cached         bool        `json:"cached,omitempty"`
	CacheExpiresAt *time.Time  `json:"cacheExpiresAt,omitempty"`
}
type Progress struct {
	Stage     string    `json:"stage"`
	Message   string    `json:"message"`
	Completed int       `json:"completed,omitempty"`
	Total     int       `json:"total,omitempty"`
	Recovery  *Recovery `json:"recovery,omitempty"`
	Preview   *Result   `json:"-"`
}
type Recovery struct {
	Stage   string `json:"stage"`
	Code    string `json:"code"`
	Attempt int    `json:"attempt"`
}
type Reporter func(Progress)
type Capabilities struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Program        string  `json:"program"`
	Cabins         []Cabin `json:"cabins"`
	VerifiedCabins []Cabin `json:"verifiedCabins"`
	MaxAdults      int     `json:"maxAdults"`
	AwardMaxAdults int     `json:"awardMaxAdults,omitempty"`
	OneWayOnly     bool    `json:"oneWayOnly"`
	Currency       string  `json:"currency"`
}

type Provider interface {
	Capabilities() Capabilities
	Search(context.Context, Query, Reporter) (Result, error)
}
type Error struct {
	Code              string `json:"code"`
	Message           string `json:"message"`
	HTTPStatus        int    `json:"httpStatus,omitempty"`
	RetryAfterSeconds int    `json:"retryAfterSeconds,omitempty"`
	Stage             string `json:"stage,omitempty"`
	OutcomeUnknown    bool   `json:"outcomeUnknown,omitempty"`
}

func (e *Error) Error() string           { return e.Message }
func Failure(code, message string) error { return &Error{Code: code, Message: message} }
