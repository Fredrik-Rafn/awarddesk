package eva

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"awarddesk/internal/award"
)

func TestAwardSearchRecoveryRechecksInventoryAndPricesOnlyMissingQuotes(t *testing.T) {
	for _, tt := range []struct {
		name                    string
		priorFlight             string
		age                     time.Duration
		bothAvailable           bool
		noAvailability          bool
		wantQuotes, wantFlights int
	}{
		{"reuse exact quote", "BR216", time.Minute, false, false, 0, 1},
		{"expired quote repriced", "BR216", 6 * time.Minute, false, false, 1, 1},
		{"missing itinerary removed", "BR226", time.Minute, false, false, 1, 1},
		{"partial quotes resumed", "BR226", time.Minute, true, false, 1, 2},
		{"inventory disappeared", "BR216", time.Minute, false, true, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			q := awardQuery()
			availability := string(awardFixture(t, "availability.html"))
			journeys := string(awardFixture(t, "journeys.html"))
			if tt.bothAvailable {
				availability = strings.ReplaceAll(availability, `alt="Ｗaiting"`, `alt="Available"`)
				journeys = strings.ReplaceAll(journeys, ">waiting<", ">Available(Unconfirmed)<")
			}
			if tt.noAvailability {
				availability = strings.ReplaceAll(availability, `alt="Available"`, `alt="Unavailable"`)
			}

			priorJourneys := strings.ReplaceAll(string(awardFixture(t, "journeys.html")), ">waiting<", ">Available(Unconfirmed)<")
			previous, err := parseAwardChoices([]byte(priorJourneys), q, map[string]bool{tt.priorFlight: true})
			if err != nil || len(previous) != 1 {
				t.Fatalf("previous choice: %v count=%d", err, len(previous))
			}
			previous[0].itinerary.Fares = []award.Fare{{Cabin: q.Cabin, PricingType: "award", Points: 12345, AvailableFor: q.Adults}}
			prior := award.Result{Query: q, FetchedAt: time.Now().Add(-tt.age), Itineraries: []award.Itinerary{previous[0].itinerary}}
			ctx := award.WithRecoveryResult(context.Background(), prior)
			availabilityChecks, journeyLoads, selections, quoteLoads := 0, 0, 0, 0
			securityCalls := 0
			p := New(Config{})
			nav := withAwardSelectionSecurity(func(_ context.Context, method, target string, values url.Values, _ string) ([]byte, string, error) {
				switch {
				case target == awardAvailabilityURL && method == "GET":
					return awardFixture(t, "availability-form.html"), target, nil
				case target == awardAvailabilityURL:
					availabilityChecks++
					if values.Get("ctl00$content$ddl_PassengerCount") != "4" {
						t.Fatal("recovery skipped actual-party availability")
					}
					return []byte(availability), target, nil
				case target == awardAirportURL:
					return awardFixture(t, "airports.xml"), target, nil
				case target == awardItineraryURL && method == "GET":
					return awardFixture(t, "itinerary.html"), target, nil
				case allowedAwardPage(target, "select-itinerary.aspx"):
					return []byte(`<form action="select-first-journey.aspx"><input name="Parameter" value="synthetic"></form>`), target, nil
				case allowedAwardPage(target, "select-first-journey.aspx") && values.Has("Parameter"):
					journeyLoads++
					return []byte(journeys), target, nil
				case allowedAwardPage(target, "select-first-journey.aspx"):
					selections++
					if availabilityChecks != 1 || journeyLoads != 1 {
						t.Fatal("pricing occurred before fresh availability and choices")
					}
					return []byte(`<form action="mileage-deducted.aspx"><input name="Parameter" value="synthetic"></form>`), target, nil
				case allowedAwardPage(target, "mileage-deducted.aspx"):
					quoteLoads++
					return awardFixture(t, "mileage.html"), target, nil
				default:
					t.Fatalf("unexpected recovery request: %s %s", method, target)
					return nil, target, nil
				}
			}, func(_ context.Context, page string, fresh []byte, rounds int) error {
				securityCalls++
				if page != awardJourneyURL || string(fresh) != journeys || rounds != 3 {
					t.Fatal("selection security lost the fresh page or required three rounds")
				}
				return nil
			})
			r, err := p.runAwardSearch(ctx, q, nav, nil)
			if err != nil || !r.Complete || len(r.Itineraries) != tt.wantFlights || selections != tt.wantQuotes || quoteLoads != tt.wantQuotes || availabilityChecks != 1 || securityCalls != tt.wantQuotes {
				t.Fatalf("result=%+v err=%v selections=%d quotes=%d availability=%d security=%d", r, err, selections, quoteLoads, availabilityChecks, securityCalls)
			}
			reused := !tt.noAvailability && (tt.bothAvailable || tt.priorFlight == "BR216") && tt.age <= 5*time.Minute
			if reused {
				if !r.FetchedAt.Equal(prior.FetchedAt) {
					t.Fatalf("recovery refreshed quote age: got %v want %v", r.FetchedAt, prior.FetchedAt)
				}

				nextCtx := award.WithRecoveryResult(context.Background(), r)
				if _, ok := reusableAwardQuote(nextCtx, q, previous[0], prior.FetchedAt.Add(5*time.Minute+time.Nanosecond)); ok {
					t.Fatal("chained recovery extended the original quote reuse window")
				}
			} else if !r.FetchedAt.After(prior.FetchedAt) {
				t.Fatal("fresh result inherited an unused quote timestamp")
			}
			for _, itinerary := range r.Itineraries {
				flight := itinerary.Segments[0].FlightNumber
				wantPoints := 70000
				if flight == tt.priorFlight && tt.age <= 5*time.Minute {
					wantPoints = 12345
				}
				if itinerary.Fares[0].Points != wantPoints || (!tt.bothAvailable && flight != "BR216") {
					t.Fatalf("old itinerary retained or wrong quote reused: %+v", itinerary)
				}
			}
		})
	}
}
