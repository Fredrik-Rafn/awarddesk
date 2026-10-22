package main

import (
	"testing"

	"awarddesk/internal/award"
	"awarddesk/internal/jobs"
)

func TestPreviewTimingsKeepFirstArrival(t *testing.T) {
	var m measurement
	observePreview(&m, jobs.Job{}, 500)
	r := award.Result{Itineraries: []award.Itinerary{{Fares: []award.Fare{{}}}}}
	observePreview(&m, jobs.Job{Result: &r}, 1000)
	r.Itineraries[0].Fares[0].MilesCash = &award.MilesCash{Miles: 123}
	observePreview(&m, jobs.Job{Result: &r}, 2000)
	observePreview(&m, jobs.Job{Result: &r}, 3000)
	if m.FirstResultsMS != 1000 || m.FirstQuoteMS != 2000 {
		t.Fatalf("incorrect first-arrival times: %+v", m)
	}
}

func TestSummaryDistinguishesRecoveredSuccessFromUserFailure(t *testing.T) {
	results := []measurement{
		{Status: "completed", Complete: true, Itineraries: 2},
		{Status: "completed", Complete: true, Itineraries: 1, Recoveries: []award.Recovery{{Code: "session_denied"}, {Code: "connection"}}},
		{Status: "completed", Complete: true, Itineraries: 0},
		{Status: "completed", Complete: false, Itineraries: 1},
		{Status: "failed", Recoveries: []award.Recovery{{Code: "connection"}}},
	}
	s := summarize(results)
	if s.Searches != 5 || s.FirstPassSuccesses != 2 || s.RecoveredSuccesses != 1 || s.FinalFailures != 1 || s.PartialResults != 1 || s.EmptySuccesses != 1 || s.RecoveryEvents != 3 || s.UserSuccessPercent != 60 {
		t.Fatalf("incorrect user-visible statistics: %+v", s)
	}
	if empty := summarize(nil); empty.UserSuccessPercent != 0 {
		t.Fatal("empty summary has a rate")
	}
}

func TestDiagnosticBatchStopsAtUncertainOrAccountFailures(t *testing.T) {
	for _, e := range []*award.Error{
		{Code: "connection", Stage: "credentials"},
		{Code: "response_read", Stage: "miles pricing"},
		{Code: "session_denied", Stage: "otp submission"},
		{Code: "rate_limited"},
	} {
		if !stopAtFailure(e) {
			t.Fatalf("batch would continue after %+v", e)
		}
	}
	for _, e := range []*award.Error{nil, {Code: "price_changed", Stage: "miles pricing"}, {Code: "search_protocol", Stage: "miles pricing"}, {Code: "session_denied", Stage: "availability"}} {
		if stopAtFailure(e) {
			t.Fatalf("batch stopped at known safe outcome: %+v", e)
		}
	}
}
