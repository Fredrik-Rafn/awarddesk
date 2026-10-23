package eva

import (
	"context"
	"net/url"
	"strings"
	"time"

	"awarddesk/internal/award"
)

type awardPartyEvidenceKey struct{}

type awardPartyEvidence struct {
	query     award.Query
	checkedAt time.Time
	positive  map[string]bool
	targets   []award.Itinerary
}

func withAwardPartyEvidence(ctx context.Context, q award.Query, checkedAt time.Time, positive map[string]bool, choices []awardChoice) context.Context {
	e := awardPartyEvidence{query: normalizedAwardRecoveryQuery(q), checkedAt: checkedAt, positive: make(map[string]bool)}
	for key, available := range positive {
		if available {
			e.positive[key] = true
		}
	}
	for _, choice := range choices {
		e.targets = append(e.targets, award.Itinerary{Segments: choice.itinerary.Segments})
	}
	e.targets = award.CloneResult(award.Result{Itineraries: e.targets}).Itineraries
	return context.WithValue(ctx, awardPartyEvidenceKey{}, e)
}

func usableAwardPartyEvidence(ctx context.Context, q award.Query, now time.Time) (map[string]bool, time.Time, bool) {
	targets, worker := ctx.Value(awardQuoteTargetsKey{}).([]AwardQuoteTarget)
	e, exists := ctx.Value(awardPartyEvidenceKey{}).(awardPartyEvidence)
	if !worker || len(targets) == 0 || !exists || q.Mode != "award" || normalizedAwardRecoveryQuery(q) != e.query || e.checkedAt.IsZero() || e.checkedAt.After(now) || now.Sub(e.checkedAt) > time.Minute {
		return nil, time.Time{}, false
	}
	for _, target := range targets {
		if len(target.Segments) == 0 {
			return nil, time.Time{}, false
		}
		var flights []string
		for _, segment := range target.Segments {
			if canonicalAwardFlight(segment.FlightNumber) == "" {
				return nil, time.Time{}, false
			}
			flights = append(flights, segment.FlightNumber)
		}
		if !e.positive[awardFlightKey(flights)] {
			return nil, time.Time{}, false
		}
		covered := false
		for _, item := range e.targets {
			covered = covered || sameAwardChoice(awardChoice{itinerary: item}, awardChoice{itinerary: award.Itinerary{Segments: target.Segments}})
		}
		if !covered {
			return nil, time.Time{}, false
		}
	}
	positive := make(map[string]bool, len(e.positive))
	for key, value := range e.positive {
		positive[key] = value
	}
	return positive, e.checkedAt, true
}

func loadAwardPartyAvailability(ctx context.Context, q award.Query, nav awardNavigation, report award.Reporter) (map[string]bool, string, time.Time, error) {
	awardProgress(report, "award availability", "Checking award seats for the requested party", 0, 0, nil)
	body, target, err := nav(ctx, "GET", awardAvailabilityURL, nil, "")
	if err != nil {
		return nil, target, time.Time{}, err
	}
	f, err := parseForm(body, target)
	if err != nil {
		return nil, target, time.Time{}, award.Failure("award_protocol", "EVA award availability form was not found")
	}
	if err = fillAwardAvailability(&f, q); err != nil {
		return nil, target, time.Time{}, err
	}
	if f.action != awardAvailabilityURL {
		u, e := url.Parse(f.action)
		if e != nil || !strings.EqualFold(u.Scheme+"://"+u.Host+u.Path, awardAvailabilityURL) {
			return nil, target, time.Time{}, award.Failure("unsafe_destination", "EVA award availability form left its expected page")
		}
	}
	awardProgress(report, "award availability", "Requesting live award seats for the selected date and party", 0, 0, nil)
	body, target, err = nav(ctx, "POST", f.action, f.values, target)
	if err != nil {
		return nil, target, time.Time{}, err
	}
	available, err := parseAwardAvailability(body, q)
	if err != nil {
		captureAwardFailure(body)
		return nil, target, time.Time{}, err
	}
	return available, target, time.Now().UTC(), nil
}
