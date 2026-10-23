package eva

import (
	"context"
	"testing"
	"time"

	"awarddesk/internal/award"
)

func recoveryAwardFixture(now time.Time) (award.Result, awardChoice) {
	q := awardQuery()
	segments := []award.Segment{{Cabin: q.Cabin, FlightNumber: "BR001", Origin: q.Origin, Destination: q.Destination, DepartureLocal: q.Date + "T15:45:00", ArrivalLocal: q.Date + "T20:15:00"}}
	i := award.Itinerary{Segments: segments, Fares: []award.Fare{{Cabin: q.Cabin, Points: 70000, PricingType: "award", AvailableFor: q.Adults}}}
	r := award.Result{Query: q, FetchedAt: now.Add(-time.Minute), Itineraries: []award.Itinerary{i}}
	choice := awardChoice{itinerary: award.Itinerary{Segments: append([]award.Segment(nil), segments...)}}
	choice.itinerary.Segments[0].FlightNumber = "BR 1"
	return r, choice
}

func TestReusableAwardQuoteRequiresFreshSameQueryAndSelectedItinerary(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		change func(*award.Result, *award.Query, *awardChoice)
		want   bool
	}{
		{"same query canonical flight", func(*award.Result, *award.Query, *awardChoice) {}, true},
		{"refresh ignored", func(_ *award.Result, q *award.Query, _ *awardChoice) { q.Refresh = true }, true},
		{"normalized query", func(r *award.Result, _ *award.Query, _ *awardChoice) {
			r.Query.Origin = " sin "
			r.Query.Provider = " EVA "
		}, true},
		{"five minute boundary", func(r *award.Result, _ *award.Query, _ *awardChoice) { r.FetchedAt = now.Add(-5 * time.Minute) }, true},
		{"stale", func(r *award.Result, _ *award.Query, _ *awardChoice) {
			r.FetchedAt = now.Add(-5*time.Minute - time.Nanosecond)
		}, false},
		{"future", func(r *award.Result, _ *award.Query, _ *awardChoice) { r.FetchedAt = now.Add(time.Nanosecond) }, false},
		{"zero time", func(r *award.Result, _ *award.Query, _ *awardChoice) { r.FetchedAt = time.Time{} }, false},
		{"cash query", func(_ *award.Result, q *award.Query, _ *awardChoice) { q.Mode = "" }, false},
		{"prior cash query", func(r *award.Result, _ *award.Query, _ *awardChoice) { r.Query.Mode = "" }, false},
		{"route", func(r *award.Result, _ *award.Query, _ *awardChoice) { r.Query.Destination = "HKG" }, false},
		{"party", func(r *award.Result, _ *award.Query, _ *awardChoice) { r.Query.Adults-- }, false},
		{"cabin", func(r *award.Result, _ *award.Query, _ *awardChoice) { r.Query.Cabin = award.Business }, false},
		{"date", func(r *award.Result, _ *award.Query, _ *awardChoice) { r.Query.Date = "2026-11-25" }, false},
		{"flight", func(_ *award.Result, _ *award.Query, c *awardChoice) { c.itinerary.Segments[0].FlightNumber = "BR2" }, false},
		{"segment cabin", func(_ *award.Result, _ *award.Query, c *awardChoice) { c.itinerary.Segments[0].Cabin = award.Business }, false},
		{"departure", func(_ *award.Result, _ *award.Query, c *awardChoice) { c.itinerary.Segments[0].DepartureLocal += "1" }, false},
		{"arrival", func(_ *award.Result, _ *award.Query, c *awardChoice) { c.itinerary.Segments[0].ArrivalLocal += "1" }, false},
		{"segment route", func(_ *award.Result, _ *award.Query, c *awardChoice) { c.itinerary.Segments[0].Origin = "HKG" }, false},
		{"missing segments", func(_ *award.Result, _ *award.Query, c *awardChoice) { c.itinerary.Segments = nil }, false},
		{"missing fare", func(r *award.Result, _ *award.Query, _ *awardChoice) { r.Itineraries[0].Fares = nil }, false},
		{"multiple fares", func(r *award.Result, _ *award.Query, _ *awardChoice) {
			r.Itineraries[0].Fares = append(r.Itineraries[0].Fares, r.Itineraries[0].Fares[0])
		}, false},
		{"wrong fare party", func(r *award.Result, _ *award.Query, _ *awardChoice) { r.Itineraries[0].Fares[0].AvailableFor-- }, false},
		{"wrong fare cabin", func(r *award.Result, _ *award.Query, _ *awardChoice) {
			r.Itineraries[0].Fares[0].Cabin = award.Business
		}, false},
		{"zero points", func(r *award.Result, _ *award.Query, _ *awardChoice) { r.Itineraries[0].Fares[0].Points = 0 }, false},
		{"cash pricing", func(r *award.Result, _ *award.Query, _ *awardChoice) {
			r.Itineraries[0].Fares[0].PricingType = "miles_cash"
		}, false},
		{"estimated", func(r *award.Result, _ *award.Query, _ *awardChoice) { r.Itineraries[0].Fares[0].Estimated = true }, false},
		{"cash component", func(r *award.Result, _ *award.Query, _ *awardChoice) {
			r.Itineraries[0].Fares[0].MilesCash = &award.MilesCash{}
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, c := recoveryAwardFixture(now)
			q := r.Query
			tt.change(&r, &q, &c)
			fare, ok := reusableAwardQuote(award.WithRecoveryResult(context.Background(), r), q, c, now)
			if ok != tt.want || (ok && fare.Points != 70000) {
				t.Fatalf("reuse=%v points=%d, want reuse=%v", ok, fare.Points, tt.want)
			}
		})
	}
	r, c := recoveryAwardFixture(now)
	if _, ok := reusableAwardQuote(context.Background(), r.Query, c, now); ok {
		t.Fatal("reused absent recovery")
	}
}

func TestAwardRecoverySnapshotsAndReturnedFaresAreIndependent(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	r, c := recoveryAwardFixture(now)
	seats, aircraft := 4, "A321"
	r.Itineraries[0].Fares[0].Seats = &seats
	r.Itineraries[0].Segments[0].Aircraft = &aircraft
	r.Warnings = []string{"original"}
	ctx := award.WithRecoveryResult(context.Background(), r)
	r.Itineraries[0].Fares[0].Points = 1
	seats = 1
	aircraft = "changed"
	r.Warnings[0] = "changed"
	snapshot, ok := award.RecoveryResult(ctx)
	if !ok || snapshot.Itineraries[0].Fares[0].Points != 70000 || *snapshot.Itineraries[0].Fares[0].Seats != 4 || *snapshot.Itineraries[0].Segments[0].Aircraft != "A321" || snapshot.Warnings[0] != "original" {
		t.Fatal("input mutations leaked into recovery snapshot")
	}
	snapshot.Itineraries[0].Fares[0].Points = 2
	*snapshot.Itineraries[0].Fares[0].Seats = 2
	fare, ok := reusableAwardQuote(ctx, snapshot.Query, c, now)
	if !ok || fare.Points != 70000 || *fare.Seats != 4 {
		t.Fatal("read mutations leaked into recovery snapshot")
	}
	*fare.Seats = 3
	again, ok := reusableAwardQuote(ctx, snapshot.Query, c, now)
	if !ok || *again.Seats != 4 {
		t.Fatal("returned fare mutations leaked into recovery snapshot")
	}
}
