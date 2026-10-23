package eva

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"awarddesk/internal/award"
)

func partyEvidenceChoices(t *testing.T) []awardChoice {
	t.Helper()
	choices, err := parseAwardChoices(awardFixture(t, "journeys.html"), awardQuery(), map[string]bool{"BR216": true})
	if err != nil || len(choices) != 1 {
		t.Fatalf("choices %d %v", len(choices), err)
	}
	return choices
}

func TestAwardHelperReusesOnlyFreshExactPartyEvidence(t *testing.T) {
	for _, tt := range []struct {
		name         string
		mutate       func(*award.Query, *time.Time, []awardChoice, []AwardQuoteTarget, map[string]bool)
		proof        bool
		wantRequests int
		wantError    bool
	}{
		{"fresh", nil, true, 0, false},
		{"missing", nil, false, 2, false},
		{"stale", func(_ *award.Query, at *time.Time, _ []awardChoice, _ []AwardQuoteTarget, _ map[string]bool) {
			*at = time.Now().Add(-61 * time.Second)
		}, true, 2, false},
		{"future", func(_ *award.Query, at *time.Time, _ []awardChoice, _ []AwardQuoteTarget, _ map[string]bool) {
			*at = time.Now().Add(time.Minute)
		}, true, 2, false},
		{"party", func(q *award.Query, _ *time.Time, _ []awardChoice, _ []AwardQuoteTarget, _ map[string]bool) {
			q.Adults = 3
		}, true, 2, false},
		{"cabin", func(q *award.Query, _ *time.Time, _ []awardChoice, _ []AwardQuoteTarget, _ map[string]bool) {
			q.Cabin = award.Business
		}, true, 2, false},
		{"date", func(q *award.Query, _ *time.Time, _ []awardChoice, _ []AwardQuoteTarget, _ map[string]bool) {
			q.Date = "2026-11-25"
		}, true, 2, false},
		{"route", func(q *award.Query, _ *time.Time, _ []awardChoice, _ []AwardQuoteTarget, _ map[string]bool) {
			q.Origin = "HKG"
		}, true, 2, false},
		{"segment changed", func(_ *award.Query, _ *time.Time, _ []awardChoice, targets []AwardQuoteTarget, _ map[string]bool) {
			targets[0].Segments[0].ArrivalLocal = "2026-11-24T20:16:00"
		}, true, 2, true},
		{"not positive", func(_ *award.Query, _ *time.Time, _ []awardChoice, _ []AwardQuoteTarget, positive map[string]bool) {
			positive["BR216"] = false
		}, true, 2, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			q := awardQuery()
			proofQuery := q
			checkedAt := time.Now().Add(-10 * time.Second).UTC()
			choices := partyEvidenceChoices(t)
			positive := map[string]bool{"BR216": true}
			copied := award.CloneResult(award.Result{Itineraries: []award.Itinerary{choices[0].itinerary}})
			targets := []AwardQuoteTarget{{Segments: copied.Itineraries[0].Segments}}
			if tt.mutate != nil {
				tt.mutate(&proofQuery, &checkedAt, choices, targets, positive)
			}
			ctx := context.Background()
			if tt.proof {
				ctx = withAwardPartyEvidence(ctx, proofQuery, checkedAt, positive, choices)
			}
			ctx = context.WithValue(ctx, awardQuoteTargetsKey{}, targets)
			requests, selections := 0, 0
			nav := func(_ context.Context, method, target string, values url.Values, _ string) ([]byte, string, error) {
				switch {
				case target == awardAvailabilityURL && method == "GET":
					requests++
					return awardFixture(t, "availability-form.html"), target, nil
				case target == awardAvailabilityURL:
					requests++
					if values.Get("ctl00$content$ddl_PassengerCount") != "4" {
						t.Fatal("fallback checked wrong party")
					}
					return awardFixture(t, "availability.html"), target, nil
				case target == awardAirportURL:
					return awardFixture(t, "airports.xml"), target, nil
				case target == awardItineraryURL && method == "GET":
					return awardFixture(t, "itinerary.html"), target, nil
				case strings.HasPrefix(target, awardItineraryURL):
					return []byte(`<form action="select-first-journey.aspx"><input name="Parameter" value="fresh"></form>`), target, nil
				case target == awardJourneyURL && values.Has("Parameter"):
					return awardFixture(t, "journeys.html"), target, nil
				case target == awardJourneyURL:
					selections++
					return []byte(`<form action="mileage-deducted.aspx"><input name="Parameter" value="fresh"></form>`), target, nil
				case allowedAwardPage(target, "mileage-deducted.aspx"):
					return awardFixture(t, "mileage.html"), target, nil
				default:
					t.Fatalf("unexpected %s %s", method, target)
					return nil, target, nil
				}
			}
			r, err := New(Config{}).runAwardSearch(ctx, q, nav, nil)
			if requests != tt.wantRequests || (err != nil) != tt.wantError {
				t.Fatalf("availability requests=%d err=%v", requests, err)
			}
			if !tt.wantError && (len(r.Itineraries) != 1 || r.Itineraries[0].Fares[0].Points != 70000 || selections != 1) {
				t.Fatalf("fresh helper quote missing %+v", r)
			}
			if tt.wantRequests == 0 && !r.FetchedAt.Equal(checkedAt) {
				t.Fatal("reused availability timestamp was refreshed")
			}
		})
	}
}

func TestAwardPartyEvidenceIsIsolatedAndIndependentOfRecoveryQuoteAge(t *testing.T) {
	q := awardQuery()
	at := time.Now().Add(-time.Second)
	choices := partyEvidenceChoices(t)
	positive := map[string]bool{"BR216": true}
	cloned := award.CloneResult(award.Result{Itineraries: []award.Itinerary{choices[0].itinerary}})
	targets := []AwardQuoteTarget{{Segments: cloned.Itineraries[0].Segments}}
	ctx := award.WithRecoveryResult(context.Background(), award.Result{Query: q, FetchedAt: time.Now().Add(-10 * time.Minute)})
	ctx = withAwardPartyEvidence(ctx, q, at, positive, choices)
	ctx = context.WithValue(ctx, awardQuoteTargetsKey{}, targets)
	positive["BR216"] = false
	choices[0].itinerary.Segments[0].ArrivalLocal = "changed"
	if choices[0].itinerary.Segments[0].Aircraft != nil {
		*choices[0].itinerary.Segments[0].Aircraft = "changed"
	}
	got, when, ok := usableAwardPartyEvidence(ctx, q, time.Now())
	if !ok || !got["BR216"] || !when.Equal(at) {
		t.Fatal("parent mutation or old recovery quote invalidated fresh evidence")
	}
	got["BR216"] = false
	if again, _, ok := usableAwardPartyEvidence(ctx, q, time.Now()); !ok || !again["BR216"] {
		t.Fatal("consumer mutated immutable evidence")
	}
	withoutWorker := withAwardPartyEvidence(context.Background(), q, at, map[string]bool{"BR216": true}, partyEvidenceChoices(t))
	if _, _, ok := usableAwardPartyEvidence(withoutWorker, q, time.Now()); ok {
		t.Fatal("parent skipped its own fresh check")
	}
	cleared := context.WithValue(ctx, awardPartyEvidenceKey{}, awardPartyEvidence{})
	if _, _, ok := usableAwardPartyEvidence(cleared, q, time.Now()); ok {
		t.Fatal("retry reused disabled evidence")
	}
}

func TestAwardPartyEvidenceRequiresEveryTargetAndFreshBookableChoices(t *testing.T) {
	q := awardQuery()
	choices := partyEvidenceChoices(t)
	at := time.Now()
	ctx := withAwardPartyEvidence(context.Background(), q, at, map[string]bool{"BR216": true}, choices)
	changed := append([]award.Segment(nil), choices[0].itinerary.Segments...)
	changed[0].FlightNumber = "BR226"
	ctx = context.WithValue(ctx, awardQuoteTargetsKey{}, []AwardQuoteTarget{{Segments: choices[0].itinerary.Segments}, {Segments: changed}})
	if _, _, ok := usableAwardPartyEvidence(ctx, q, time.Now()); ok {
		t.Fatal("partially covered assignment reused inventory")
	}
	ctx = context.WithValue(ctx, awardQuoteTargetsKey{}, []AwardQuoteTarget{{Segments: choices[0].itinerary.Segments}})
	waiting := []byte(strings.ReplaceAll(string(awardFixture(t, "journeys.html")), ">Available(Unconfirmed)<", ">waiting<"))
	if selected, err := parseAwardChoices(waiting, q, map[string]bool{"BR216": true}); err != nil || len(awardWorkerChoices(ctx, selected)) != 0 {
		t.Fatal("parent evidence promoted fresh waiting choice")
	}
}
