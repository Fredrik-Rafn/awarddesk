package fleet

import (
	"context"
	"testing"

	"awarddesk/internal/award"
)

func TestAwardFleetRecoveryContextStartsAfterPartialAndIsIsolated(t *testing.T) {
	f, providers := setup(t, 3)
	partial := awardRecoveryResult("quoted")
	seats := 4
	partial.Itineraries[0].Fares[0].Seats = &seats
	providers["a"].run = func(ctx context.Context) (award.Result, error) {
		if _, ok := award.RecoveryResult(ctx); ok {
			t.Fatal("first account received recovery")
		}
		return partial, &award.Error{Code: "session_denied", Stage: "award pricing", HTTPStatus: 403}
	}
	providers["b"].run = func(ctx context.Context) (award.Result, error) {
		r, ok := award.RecoveryResult(ctx)
		if !ok || len(r.Itineraries) != 1 || r.Itineraries[0].ID != "quoted" {
			t.Fatal("second account missing partial recovery")
		}
		r.Itineraries[0].Fares[0].Points = 1
		*r.Itineraries[0].Fares[0].Seats = 1
		again, _ := award.RecoveryResult(ctx)
		if again.Itineraries[0].Fares[0].Points != 17500 || *again.Itineraries[0].Fares[0].Seats != 4 {
			t.Fatal("recovery reads share mutable state")
		}
		return award.Result{Query: partial.Query}, &award.Error{Code: "session_expired", Stage: "award availability"}
	}
	providers["c"].run = func(ctx context.Context) (award.Result, error) {
		r, ok := award.RecoveryResult(ctx)
		if !ok || r.Itineraries[0].Fares[0].Points != 17500 || *r.Itineraries[0].Fares[0].Seats != 4 {
			t.Fatal("later account lost original recovery")
		}
		return award.Result{Query: partial.Query, Complete: true}, nil
	}
	_, err := f.Search(context.Background(), partial.Query, nil)
	if err != nil {
		t.Fatal(err)
	}
	if partial.Itineraries[0].Fares[0].Points != 17500 || seats != 4 {
		t.Fatal("provider mutated original result through recovery")
	}
	if providers["c"].calls.Load() != 1 {
		t.Fatal("final recovery account was not called")
	}
}

func TestAwardFleetEmptyFailureDoesNotCreateRecoveryContext(t *testing.T) {
	f, providers := setup(t, 2)
	q := award.Query{Mode: "award", Refresh: true}
	providers["a"].run = func(ctx context.Context) (award.Result, error) {
		if _, ok := award.RecoveryResult(ctx); ok {
			t.Fatal("first account received recovery")
		}
		return award.Result{Query: q}, &award.Error{Code: "session_denied", Stage: "award availability", HTTPStatus: 403}
	}
	providers["b"].run = func(ctx context.Context) (award.Result, error) {
		if _, ok := award.RecoveryResult(ctx); ok {
			t.Fatal("empty failure created recovery")
		}
		return award.Result{Query: q, Complete: true}, nil
	}
	if _, err := f.Search(context.Background(), q, nil); err != nil {
		t.Fatal(err)
	}
	if providers["b"].calls.Load() != 1 {
		t.Fatal("second account was not called")
	}
}
