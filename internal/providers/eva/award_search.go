package eva

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"awarddesk/internal/antibot"
	"awarddesk/internal/award"
	"awarddesk/internal/session"
)

const awardTicketPath = "/flyeva/EVA/B2C/plan-your-journey/online-reservation/award-ticket/"
const awardItineraryURL = "https://booking.evaair.com" + awardTicketPath + "select-itinerary.aspx"
const awardJourneyURL = "https://booking.evaair.com" + awardTicketPath + "select-first-journey.aspx"
const awardAirportURL = "https://booking.evaair.com/FLYEVA/handler/getAirportCache.ashx?action=05"

type awardNavigation func(context.Context, string, string, url.Values, string) ([]byte, string, error)

type awardAirport struct {
	Code      string `xml:"value"`
	Continent string `xml:"continent"`
	Community string `xml:"community"`
}

type awardAirportDirectory struct {
	FetchedAt time.Time
	Airports  []awardAirport `xml:"airport"`
}

func (p *Provider) searchAward(ctx context.Context, q award.Query, report award.Reporter) (award.Result, error) {
	result := award.Result{Query: q, Itineraries: []award.Itinerary{}}
	if err := q.Validate(time.Now()); err != nil {
		return result, err
	}
	if q.Provider != "eva" || q.Cabin == award.First {
		return result, award.Failure("unsupported", "Unsupported EVA award search")
	}
	stage := "session preparation"
	ctx, report = trackRequestRecoveries(ctx, report)
	observer := report
	report = func(pr award.Progress) {
		stage = pr.Stage
		if observer != nil {
			observer(pr)
		}
	}
	err := p.withSession(ctx, report, func(c *session.Client, proxy string) error {
		nav := withAwardSelectionSecurity(func(ctx context.Context, method, target string, values url.Values, from string) ([]byte, string, error) {
			return p.awardNavigate(ctx, c, proxy, method, target, values, from, report)
		}, func(ctx context.Context, page string, body []byte, rounds int) error {

			awardProgress(report, "award pricing", "Preparing security for the current award flight selection", 0, 0, nil)
			return (antibot.Hyper{Key: p.config.HyperKey, MinimumSensorRounds: rounds}).Prepare(ctx, c, page, body)
		})
		var err error
		result, err = runAwardAttempts(ctx, q, func(attemptCtx context.Context) (award.Result, error) {
			return p.runAwardSearch(attemptCtx, q, nav, report)
		}, func() string { return stage }, report)
		return err
	})
	err = loginStageError(err, stage)
	var failure *award.Error
	if errors.As(err, &failure) && failure.Code == "award_protocol" {

		slog.Warn("EVA award response was not recognized", "stage", failure.Stage, "reason", failure.Message,
			"origin", q.Origin, "destination", q.Destination, "date", q.Date, "cabin", q.Cabin, "party", q.Adults)
	}
	return result, err
}

func awardProgress(report award.Reporter, stage, message string, done, total int, result *award.Result) {
	if report != nil {
		report(award.Progress{Stage: stage, Message: message, Completed: done, Total: total, Preview: result})
	}
}

func (p *Provider) runAwardSearch(ctx context.Context, q award.Query, nav awardNavigation, report award.Reporter) (award.Result, error) {
	r := award.Result{Query: q, Itineraries: []award.Itinerary{}}
	available, availabilityCheckedAt, reusedInventory := usableAwardPartyEvidence(ctx, q, time.Now())
	target := awardAvailabilityURL
	var err error
	if !reusedInventory {
		available, target, availabilityCheckedAt, err = loadAwardPartyAvailability(ctx, q, nav, report)
		if err != nil {
			return r, err
		}
	} else {
		targets, _ := ctx.Value(awardQuoteTargetsKey{}).([]AwardQuoteTarget)
		slog.Info("EVA award helper reused parent inventory", "origin", q.Origin, "destination", q.Destination, "date", q.Date, "cabin", q.Cabin, "party", q.Adults, "age", time.Since(availabilityCheckedAt).Round(time.Millisecond), "targets", len(targets))
		awardProgress(report, "award availability", "Using the current search's freshly checked party availability", 0, 0, nil)
	}
	r.FetchedAt = availabilityCheckedAt
	if len(available) == 0 {
		r.Complete = true
		return r, nil
	}
	if p.awardAirports == nil || time.Since(p.awardAirports.FetchedAt) >= 24*time.Hour {
		raw, _, e := nav(ctx, "GET", awardAirportURL, nil, target)
		if e != nil {
			return r, e
		}
		var directory awardAirportDirectory
		if xml.Unmarshal(raw, &directory) != nil || len(directory.Airports) == 0 {
			captureAwardFailure(raw)
			return r, award.Failure("award_protocol", "EVA award airport metadata changed")
		}
		directory.FetchedAt = time.Now()
		p.awardAirports = &directory
	}
	awardProgress(report, "award flights", "Loading available award itineraries", 0, 0, nil)
	body, target, err := p.loadAwardJourneys(ctx, q, nav)
	if err != nil {
		return r, err
	}
	choices, err := parseAwardChoices(body, q, available)
	if err != nil {
		captureAwardFailure(body)
		return r, err
	}
	choices = awardWorkerChoices(ctx, choices)

	if len(choices) == 0 {
		return r, award.Failure("award_availability_changed", "Award availability changed before pricing; no matching bookable flight remained")
	}
	if !reusedInventory {
		ctx = withAwardPartyEvidence(ctx, q, availabilityCheckedAt, available, choices)
	}
	transfers := map[string]bool{}
	for _, choice := range choices {
		for i := 1; i < len(choice.itinerary.Segments); i++ {
			previous, next := choice.itinerary.Segments[i-1], choice.itinerary.Segments[i]
			if previous.Destination != next.Origin {
				key := previous.Destination + "/" + next.Origin
				if !transfers[key] {
					r.Warnings = append(r.Warnings, fmt.Sprintf("Some award itineraries require a self-transfer from %s to %s; ground transport is not included.", previous.Destination, next.Origin))
					transfers[key] = true
				}
			}
		}
	}

	unpriced := make([]awardChoice, 0, len(choices))
	for _, choice := range choices {
		if fare, ok := reusableAwardQuote(ctx, q, choice, time.Now()); ok {
			choice.itinerary.Fares = []award.Fare{fare}
			r.Itineraries = append(r.Itineraries, choice.itinerary)
		} else {
			unpriced = append(unpriced, choice)
		}
	}
	reused := len(r.Itineraries)
	if reused > 0 {

		if prior, ok := award.RecoveryResult(ctx); ok && prior.FetchedAt.Before(r.FetchedAt) {
			r.FetchedAt = prior.FetchedAt
		}
		awardProgress(report, "award quote reuse", "Fresh availability confirmed; retaining exact mileage already retrieved during this search", reused, len(choices), &r)
	}
	quoted, rebuildJourneys := false, false
	price := func(pending []awardChoice) error {
		for _, original := range pending {
			if err = ctx.Err(); err != nil {
				return err
			}
			awardProgress(report, "award pricing", "Retrieving exact award mileage for each eligible itinerary", len(r.Itineraries), len(choices), &r)
			choice := original
			if quoted {
				var fresh []awardChoice
				body, target, fresh, err = p.refreshAwardChoices(ctx, q, nav, available, target, &rebuildJourneys)
				if err != nil {
					return err
				}

				found := false
				for _, candidate := range fresh {
					if sameAwardChoice(candidate, original) {
						choice, found = candidate, true
						break
					}
				}
				if !found {
					return award.Failure("award_availability_changed", "EVA changed an award itinerary during pricing")
				}
			}
			f, err := parseForm(body, target)
			if err != nil || !allowedAwardPage(f.action, "select-first-journey.aspx") {
				return award.Failure("award_protocol", "EVA award selection form changed")
			}
			if err = selectAwardChoice(&f, choice); err != nil {
				return err
			}
			quote, next, e := nav(ctx, "POST", f.action, f.values, target)
			if e != nil {
				return e
			}
			quote, next, e = followAwardBridge(ctx, quote, next, "mileage-deducted.aspx", nav)
			if e != nil {
				return e
			}
			if !allowedAwardPage(next, "mileage-deducted.aspx") {
				return award.Failure("award_protocol", "EVA did not return award mileage pricing")
			}
			points, e := parseAwardMileage(quote, choice, q)
			if e != nil {
				return e
			}
			choice.itinerary.Fares = []award.Fare{{Name: "Award ticket", Cabin: q.Cabin, Points: points, Program: "EVA Infinity MileageLands", PricingType: "award", AvailableFor: q.Adults}}
			r.Itineraries = append(r.Itineraries, choice.itinerary)
			quoted = true
			target = next
			awardProgress(report, "award pricing", "Award mileage retrieved; taxes and fees are not quoted", len(r.Itineraries), len(choices), &r)
		}
		return nil
	}
	if err = priceAwardWithWorkers(ctx, q, unpriced, &r, price, report); err != nil {
		return r, err
	}

	orderAwardItineraries(&r, choices)
	r.Complete = true
	return r, nil
}

func sameAwardChoice(a, b awardChoice) bool {
	if len(a.itinerary.Segments) != len(b.itinerary.Segments) {
		return false
	}
	for i, s := range a.itinerary.Segments {
		t := b.itinerary.Segments[i]
		if s.Cabin != t.Cabin || canonicalAwardFlight(s.FlightNumber) != canonicalAwardFlight(t.FlightNumber) || s.Origin != t.Origin || s.Destination != t.Destination || s.DepartureLocal != t.DepartureLocal || s.ArrivalLocal != t.ArrivalLocal {
			return false
		}
	}
	return true
}
