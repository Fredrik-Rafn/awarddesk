// Package award defines the airline-independent search contract.
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
	Refresh     bool   `json:"refresh,omitempty"`
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
	return nil
}

// Money preserves the provider's decimal amount without binary float rounding.
type Money struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// Fare is an award quote for the search party. Seats is the count displayed by
// the provider (which may be capped), not a guarantee of total inventory.
type Fare struct {
	Name      string `json:"name"`
	Cabin     Cabin  `json:"cabin"`
	Points    int    `json:"points"`
	Program   string `json:"program"`
	Taxes     Money  `json:"taxes"`
	Seats     *int   `json:"seats,omitempty"`
	Estimated bool   `json:"estimated"`
	// CashPrice is the full cash fare, not the cash remainder of a redemption.
	CashPrice         *Money     `json:"cashPrice,omitempty"`
	PricingType       string     `json:"pricingType,omitempty"`
	MilesCash         *MilesCash `json:"milesCash,omitempty"`
	QuoteID           string     `json:"-"`
	MilesCashEligible *bool      `json:"milesCashEligible,omitempty"`
}

type MilesCash struct {
	Miles         int   `json:"miles"`
	RemainingCash Money `json:"remainingCash"`
	Selectable    bool  `json:"selectable"`
}

// Segment times are local airport wall times, without a fabricated UTC offset.
// Aircraft is nil when the airline does not expose a usable value.
type Segment struct {
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

// Result.Complete concerns the recognized search and pricing response. Optional
// details and later checkout offers can remain unavailable; see Warnings.
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
	// Preview is borrowed for this synchronous callback; consumers must clone
	// it before retaining it. It is exposed through Job.Result, not progress JSON.
	Preview *Result `json:"-"`
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
	OneWayOnly     bool    `json:"oneWayOnly"`
	Currency       string  `json:"currency"`
}

// Provider implementations must be safe to reuse across searches, keep session
// state per call, honor cancellation, and stop mutating results before returning.
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
