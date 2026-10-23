package eva

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"awarddesk/internal/award"
)

func TestAwardProofDirectSetupFailureRestartsFullFreshAvailabilityOnce(t *testing.T) {
	q := awardQuery()
	choices := partyEvidenceChoices(t)
	ctx := withAwardPartyEvidence(context.Background(), q, time.Now().Add(-time.Second), map[string]bool{"BR216": true}, choices)
	ctx = context.WithValue(ctx, awardQuoteTargetsKey{}, []AwardQuoteTarget{{Segments: choices[0].itinerary.Segments}})
	p := New(Config{})
	availabilityRequests, itineraryGets, selectionPosts := 0, 0, 0
	stage := ""
	var recoveries []*award.Recovery
	report := func(pr award.Progress) {
		stage = pr.Stage
		if pr.Recovery != nil {
			recoveries = append(recoveries, pr.Recovery)
		}
	}
	nav := func(_ context.Context, method, target string, values url.Values, _ string) ([]byte, string, error) {
		switch {
		case target == awardAvailabilityURL && method == "GET":
			availabilityRequests++
			return awardFixture(t, "availability-form.html"), target, nil
		case target == awardAvailabilityURL:
			availabilityRequests++
			if values.Get("ctl00$content$ddl_PassengerCount") != "4" {
				t.Fatal("wrong fallback party")
			}
			return awardFixture(t, "availability.html"), target, nil
		case target == awardAirportURL:
			return awardFixture(t, "airports.xml"), target, nil
		case target == awardItineraryURL && method == "GET":
			itineraryGets++
			if itineraryGets == 1 {
				return nil, target, &award.Error{Code: "session_denied", HTTPStatus: 403}
			}
			return awardFixture(t, "itinerary.html"), target, nil
		case strings.HasPrefix(target, awardItineraryURL):
			return []byte(`<form action="select-first-journey.aspx"><input name="Parameter" value="new-handoff"></form>`), target, nil
		case target == awardJourneyURL && values.Has("Parameter"):
			return []byte(strings.ReplaceAll(string(awardFixture(t, "journeys.html")), "fresh-test-state", "second-attempt-state")), target, nil
		case target == awardJourneyURL:
			selectionPosts++
			if values.Get("__ZIPSTATE") != "second-attempt-state" {
				t.Fatal("stale selection form replayed")
			}
			return []byte(`<form action="mileage-deducted.aspx"><input name="Parameter" value="new-quote"></form>`), target, nil
		case allowedAwardPage(target, "mileage-deducted.aspx"):
			return awardFixture(t, "mileage.html"), target, nil
		default:
			t.Fatalf("unexpected %s %s", method, target)
			return nil, target, nil
		}
	}
	r, err := runAwardAttempts(ctx, q, func(ctx context.Context) (award.Result, error) { return p.runAwardSearch(ctx, q, nav, report) }, func() string { return stage }, report)
	if err != nil || !r.Complete || len(r.Itineraries) != 1 || availabilityRequests != 2 || itineraryGets != 2 || selectionPosts != 1 || len(recoveries) != 1 || recoveries[0].Code != "session_denied" || recoveries[0].Stage != "award flights" {
		t.Fatalf("err=%v availability=%d setup=%d selections=%d recoveries=%+v", err, availabilityRequests, itineraryGets, selectionPosts, recoveries)
	}
}

func TestAwardProofFallbackExclusionsAndSharedAttemptBudget(t *testing.T) {
	for _, tt := range []struct {
		name, stage, code string
		unknown, partial  bool
		want              int
	}{
		{"setup", "award flights", "award_protocol", false, false, 2},
		{"pricing", "award pricing", "award_protocol", false, false, 1},
		{"credentials", "credentials", "award_protocol", false, false, 1},
		{"notready", "award flights", "award_helper_not_ready", false, false, 1},
		{"expired auth", "award flights", "session_expired", false, false, 1},
		{"unknown", "award flights", "award_protocol", true, false, 1},
		{"partial", "award flights", "award_protocol", false, true, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			q := awardQuery()
			choices := partyEvidenceChoices(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			deadline, _ := ctx.Deadline()
			ctx = withAwardPartyEvidence(ctx, q, time.Now(), map[string]bool{"BR216": true}, choices)
			ctx = context.WithValue(ctx, awardQuoteTargetsKey{}, []AwardQuoteTarget{{Segments: choices[0].itinerary.Segments}})
			calls := 0
			_, _ = runAwardAttempts(ctx, q, func(attemptCtx context.Context) (award.Result, error) {
				calls++
				if got, _ := attemptCtx.Deadline(); !got.Equal(deadline) {
					t.Fatal("retry extended helper budget")
				}
				_, _, proof := usableAwardPartyEvidence(attemptCtx, q, time.Now())
				if proof != (calls == 1) {
					t.Fatal("fallback failed to disable old proof")
				}
				r := award.Result{Query: q}
				if tt.partial {
					r.Itineraries = []award.Itinerary{choices[0].itinerary}
				}
				return r, &award.Error{Code: tt.code, OutcomeUnknown: tt.unknown}
			}, func() string { return tt.stage }, nil)
			if calls != tt.want {
				t.Fatalf("attempts=%d want%d", calls, tt.want)
			}
		})
	}
}
