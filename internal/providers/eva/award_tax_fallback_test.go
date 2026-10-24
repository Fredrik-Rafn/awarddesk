package eva

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"awarddesk/internal/award"
)

type absentTaxSession struct{ writes int }

func (*absentTaxSession) ReadState(string) ([]byte, error)  { return nil, os.ErrNotExist }
func (b *absentTaxSession) WriteState(string, []byte) error { b.writes++; return nil }
func (b *absentTaxSession) DeleteState(string) error        { b.writes++; return nil }
func (*absentTaxSession) LockState(string) (func(), error)  { return func() {}, nil }

func TestAwardTaxHelperNeverStartsFreshLoginWithoutSavedSession(t *testing.T) {
	r, _, _, _, _ := awardTaxFlowFixture(t)
	backend := &absentTaxSession{}
	p := New(Config{StateBackend: backend})
	got, err := p.SearchAwardTaxEstimates(context.Background(), r, nil)
	var failure *award.Error
	if !errors.As(err, &failure) || failure.Code != "award_helper_not_ready" || backend.writes != 0 || !reflect.DeepEqual(got, r) {
		t.Fatal("helper attempted session setup or changed completed award data")
	}
}

func TestAwardTaxFallbackKeepsAwardDataAndMergesExactPartialDespiteFailure(t *testing.T) {
	r, _, _, _, _ := awardTaxFlowFixture(t)
	before := award.CloneResult(r)
	var returned award.Result
	var recoveryCount int
	ctx := WithAwardTaxWorker(context.Background(), func(ctx context.Context, input award.Result, report award.Reporter) (award.Result, error) {
		if !reflect.DeepEqual(input, before) {
			t.Fatal("worker did not receive the completed award snapshot")
		}
		input.Itineraries[0].Fares[0].TaxEstimate = &award.TaxEstimate{Total: award.Money{Amount: "42", Currency: "TWD"}, Source: "eva_cash_availability", QuotedAt: time.Now().UTC()}
		input.Itineraries[0].Summary = "child summary"
		input.FetchedAt = time.Now().Add(time.Hour)
		input.Warnings = []string{"child warning"}
		report(award.Progress{Preview: &input})
		report(award.Progress{Preview: &input, Recovery: &award.Recovery{Stage: "availability", Code: "connection", Attempt: 1}})
		returned = input
		return input, award.Failure("connection", "interrupted after verified estimate")
	})
	got := recoverAwardTaxEstimates(ctx, r, func(pr award.Progress) {
		recoveryCount++
		if pr.Recovery == nil || pr.Preview == nil || pr.Preview.Itineraries[0].Summary != before.Itineraries[0].Summary {
			t.Fatal("child preview escaped or recovery missing")
		}
	})
	if recoveryCount != 2 || got.Itineraries[0].Fares[0].TaxEstimate == nil {
		t.Fatal("verified partial or actual recovery lost")
	}
	returned.Itineraries[0].Fares[0].TaxEstimate.Total.Amount = "999"
	if got.Itineraries[0].Fares[0].TaxEstimate.Total.Amount != "42" || !reflect.DeepEqual(r, before) {
		t.Fatal("worker snapshot mutation escaped")
	}
	got.Itineraries[0].Fares[0].TaxEstimate = nil
	if !reflect.DeepEqual(got, before) {
		t.Fatal("award data changed during optional enrichment")
	}
}

func TestAwardTaxFallbackRejectsUnrelatedOrInvalidEstimates(t *testing.T) {
	now := time.Now().UTC()
	for _, tt := range []struct {
		name   string
		change func(*award.Result)
	}{
		{"party", func(r *award.Result) { r.Query.Adults++ }},
		{"route", func(r *award.Result) { r.Query.Destination = "ICN" }},
		{"date", func(r *award.Result) { r.Query.Date = "2027-01-01" }},
		{"cabin", func(r *award.Result) { r.Query.Cabin = award.Business }},
		{"flight", func(r *award.Result) { r.Itineraries[0].Segments[0].FlightNumber = "BR999" }},
		{"time", func(r *award.Result) { r.Itineraries[0].Segments[0].DepartureLocal += "1" }},
		{"points", func(r *award.Result) { r.Itineraries[0].Fares[0].Points++ }},
		{"cash fare", func(r *award.Result) { r.Itineraries[0].Fares[0].PricingType = "cash" }},
		{"estimated", func(r *award.Result) { r.Itineraries[0].Fares[0].Estimated = true }},
		{"stale", func(r *award.Result) { r.Itineraries[0].Fares[0].TaxEstimate.QuotedAt = now.Add(-6 * time.Minute) }},
		{"future", func(r *award.Result) { r.Itineraries[0].Fares[0].TaxEstimate.QuotedAt = now.Add(time.Second) }},
		{"invalid amount", func(r *award.Result) { r.Itineraries[0].Fares[0].TaxEstimate.Total.Amount = "-1" }},
		{"invalid currency", func(r *award.Result) { r.Itineraries[0].Fares[0].TaxEstimate.Total.Currency = "?" }},
		{"unknown source", func(r *award.Result) { r.Itineraries[0].Fares[0].TaxEstimate.Source = "other" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, _, _, _, _ := awardTaxFlowFixture(t)
			child := award.CloneResult(r)
			child.Itineraries[0].Fares[0].TaxEstimate = &award.TaxEstimate{Total: award.Money{Amount: "42", Currency: "TWD"}, Source: "eva_cash_fare", QuotedAt: now}
			tt.change(&child)
			before := award.CloneResult(r)
			if mergeAwardTaxEstimates(&r, child, now) != 0 || !reflect.DeepEqual(r, before) {
				t.Fatal("unrelated estimate changed award result")
			}
		})
	}
}

func TestAwardTaxFallbackSkipsCompleteTaxDataAndCancelledRequests(t *testing.T) {
	r, _, _, _, _ := awardTaxFlowFixture(t)
	calls := 0
	ctx := WithAwardTaxWorker(context.Background(), func(context.Context, award.Result, award.Reporter) (award.Result, error) {
		calls++
		return award.Result{}, nil
	})
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	recoverAwardTaxEstimates(cancelled, r, nil)
	r.Itineraries[0].Fares[0].Taxes = award.Money{Amount: "2", Currency: "TWD"}
	recoverAwardTaxEstimates(ctx, r, nil)
	if calls != 0 {
		t.Fatal("unnecessary independent account request")
	}
}
