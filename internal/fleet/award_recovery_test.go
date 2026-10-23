package fleet

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"awarddesk/internal/award"
	"awarddesk/internal/storage"
)

func awardRecoveryResult(ids ...string) award.Result {
	r := award.Result{Query: award.Query{Mode: "award", Refresh: true}}
	for _, id := range ids {
		r.Itineraries = append(r.Itineraries, award.Itinerary{ID: id, Fares: []award.Fare{{Points: 17500, PricingType: "award"}}})
	}
	return r
}

func TestAwardPricingPartialFailsOverToFreshCompleteResult(t *testing.T) {
	f, providers := setup(t, 2)
	partial := awardRecoveryResult("old-flight")
	complete := awardRecoveryResult("new-flight")
	complete.Complete = true
	providers["a"].run = func(context.Context) (award.Result, error) {
		return partial, &award.Error{Code: "session_denied", Stage: "award pricing", HTTPStatus: 403}
	}
	providers["b"].run = func(context.Context) (award.Result, error) { return complete, nil }
	var recoveries []award.Recovery
	r, err := f.Search(context.Background(), partial.Query, func(p award.Progress) {
		if p.Recovery != nil {
			recoveries = append(recoveries, *p.Recovery)
		}
	})
	if err != nil || !reflect.DeepEqual(r, complete) {
		t.Fatalf("fresh complete result was not returned without merging old quotes: result=%+v err=%v", r, err)
	}
	if providers["a"].calls.Load() != 1 || providers["b"].calls.Load() != 1 || len(recoveries) != 1 || recoveries[0].Stage != "award pricing" || recoveries[0].Code != "session_denied" || recoveries[0].Attempt != 1 {
		t.Fatalf("missing bounded transparent recovery: %+v", recoveries)
	}
}

func TestAwardBestPartialSurvivesLaterEmptyFailures(t *testing.T) {
	f, providers := setup(t, 3)
	partial := awardRecoveryResult("first", "second")
	firstError := &award.Error{Code: "session_expired", Stage: "award pricing"}
	lastError := &award.Error{Code: "session_denied", Stage: "award availability", HTTPStatus: 403}
	providers["a"].run = func(context.Context) (award.Result, error) { return partial, firstError }
	for _, id := range []string{"b", "c"} {
		providers[id].run = func(context.Context) (award.Result, error) { return award.Result{}, lastError }
	}
	r, err := f.Search(context.Background(), partial.Query, nil)
	if err != lastError || r.Complete || !reflect.DeepEqual(r.Itineraries, partial.Itineraries) {
		t.Fatalf("best partial lost: result=%+v err=%v", r, err)
	}
	for id, p := range providers {
		if p.calls.Load() != 1 {
			t.Fatalf("account %s called %d times", id, p.calls.Load())
		}
	}
}

func TestAwardUncertainLoginAndOTPFailuresNeverFailOver(t *testing.T) {
	for _, failure := range []*award.Error{
		{Code: "connection", Stage: "award pricing", OutcomeUnknown: true},
		{Code: "session_expired", Stage: "credentials"},
		{Code: "connection", Stage: "otp"},
		{Code: "session_denied", Stage: "otp submission"},
		{Code: "connection", Stage: "verification continuation", OutcomeUnknown: true},
	} {
		t.Run(failure.Stage, func(t *testing.T) {
			f, providers := setup(t, 2)
			partial := awardRecoveryResult("quoted")
			providers["a"].run = func(context.Context) (award.Result, error) { return partial, failure }
			r, err := f.Search(context.Background(), partial.Query, nil)
			if err != failure || providers["a"].calls.Load() != 1 || providers["b"].calls.Load() != 0 || !reflect.DeepEqual(r.Itineraries, partial.Itineraries) {
				t.Fatalf("unsafe operation retried or partial lost: result=%+v err=%v", r, err)
			}
		})
	}
}

func TestAwardFailoverUsesAtMostFiveDistinctAccounts(t *testing.T) {
	f, providers := setup(t, 7)
	for _, p := range providers {
		p.run = func(context.Context) (award.Result, error) {
			return award.Result{}, &award.Error{Code: "session_denied", Stage: "award pricing", HTTPStatus: 403}
		}
	}
	var notices int
	_, err := f.Search(context.Background(), award.Query{Mode: "award", Refresh: true}, func(p award.Progress) {
		if p.Recovery != nil {
			notices++
		}
	})
	var calls int32
	for id, p := range providers {
		if p.calls.Load() > 1 {
			t.Fatalf("account %s replayed", id)
		}
		calls += p.calls.Load()
	}
	if err == nil || calls != 5 || notices != 4 {
		t.Fatalf("unbounded/missing account recovery: calls=%d notices=%d err=%v", calls, notices, err)
	}
}

func TestAwardExplicitRejectionAndReadOnlySetupCanUseAnotherAccount(t *testing.T) {
	for _, failure := range []*award.Error{
		{Code: "captcha_incorrect", Stage: "credentials"},
		{Code: "session_denied", Stage: "credentials", HTTPStatus: 403},
		{Code: "connection", Stage: "login page"},
		{Code: "connection", Stage: "adaptive security"},
		{Code: "mailbox", Stage: "mailbox"},
		{Code: "challenge_rejected", Stage: "award flights"},
	} {
		t.Run(failure.Code+failure.Stage, func(t *testing.T) {
			f, providers := setup(t, 2)
			providers["a"].run = func(context.Context) (award.Result, error) { return award.Result{}, failure }
			_, err := f.Search(context.Background(), award.Query{Mode: "award", Refresh: true}, nil)
			if err != nil || providers["a"].calls.Load() != 1 || providers["b"].calls.Load() != 1 {
				t.Fatalf("safe alternate account missing: %v", err)
			}
			uncertain := *failure
			uncertain.OutcomeUnknown = true
			if safeAwardFailover(&uncertain) {
				t.Fatal("uncertain authentication was retried")
			}
		})
	}
}

func TestAwardCancellationStopsClaimAndSearchRecovery(t *testing.T) {
	t.Run("before claim", func(t *testing.T) {
		f, providers := setup(t, 2)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := f.Search(ctx, award.Query{Mode: "award", Refresh: true}, nil)
		if !errors.Is(err, context.Canceled) || providers["a"].calls.Load()+providers["b"].calls.Load() != 0 {
			t.Fatalf("cancelled claim executed: %v", err)
		}
	})
	t.Run("between accounts", func(t *testing.T) {
		f, providers := setup(t, 2)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		partial := awardRecoveryResult("quoted")
		providers["a"].run = func(context.Context) (award.Result, error) {
			return partial, &award.Error{Code: "session_denied", Stage: "award pricing", HTTPStatus: 403}
		}
		r, err := f.Search(ctx, partial.Query, func(p award.Progress) {
			if p.Recovery != nil {
				cancel()
			}
		})
		if !errors.Is(err, context.Canceled) || providers["b"].calls.Load() != 0 || !reflect.DeepEqual(r.Itineraries, partial.Itineraries) {
			t.Fatalf("prior account error masked cancellation or partial lost: result=%+v err=%v", r, err)
		}
	})
	t.Run("during search", func(t *testing.T) {
		f, providers := setup(t, 2)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		providers["a"].run = func(ctx context.Context) (award.Result, error) {
			cancel()
			return award.Result{}, ctx.Err()
		}
		_, err := f.Search(ctx, award.Query{Mode: "award", Refresh: true}, nil)
		if !errors.Is(err, context.Canceled) || providers["a"].calls.Load() != 1 || providers["b"].calls.Load() != 0 {
			t.Fatalf("cancelled search retried: %v", err)
		}
	})
}

func TestAwardQueryFailuresDoNotPenalizeAccounts(t *testing.T) {
	for _, code := range []string{"award_protocol", "award_availability_changed", "search_mismatch", "unsupported_route", "unsupported", "award_limit", "session_denied"} {
		t.Run(code, func(t *testing.T) {
			f, providers := setup(t, 1)
			providers["a"].run = func(context.Context) (award.Result, error) {
				return award.Result{}, &award.Error{Code: code, Stage: "award pricing"}
			}
			_, err := f.Search(context.Background(), award.Query{Mode: "award", Refresh: true}, nil)
			if err == nil {
				t.Fatal("query error incorrectly became success")
			}
			var status Status
			f.db.RuntimeValue("account:a", &status)
			if code == "session_denied" {
				if status.State != "failed" || status.ConsecutiveFailures != 1 || status.NextAttempt.IsZero() {
					t.Fatalf("session rejection did not penalize account: %+v", status)
				}
			} else if status.State != "ready" || status.ConsecutiveFailures != 0 || !status.NextAttempt.IsZero() || status.NeedsAttention {
				t.Fatalf("query failure quarantined healthy account: %+v", status)
			}
		})
	}
}

type awardPreviewAirline struct {
	*fakeAirline
	preview award.Result
}

func (p *awardPreviewAirline) Search(ctx context.Context, q award.Query, report award.Reporter) (award.Result, error) {
	report(award.Progress{Stage: "award pricing", Preview: &p.preview})
	return p.fakeAirline.Search(ctx, q, report)
}

func TestAwardRecoveryDoesNotReplacePreviewWithLessCompleteData(t *testing.T) {
	f, providers := setup(t, 2)
	partial := awardRecoveryResult("quoted")
	providers["a"].run = func(context.Context) (award.Result, error) {
		return partial, &award.Error{Code: "session_expired", Stage: "award pricing"}
	}
	providers["b"].run = func(context.Context) (award.Result, error) {
		return award.Result{}, &award.Error{Code: "session_denied", Stage: "award pricing"}
	}
	f.build = func(a storage.Account, _ map[string]string) airline {
		preview := partial
		if a.ID == "b" {
			preview = award.Result{}
		}
		return &awardPreviewAirline{fakeAirline: providers[a.ID], preview: preview}
	}
	var previewCounts []int
	_, _ = f.Search(context.Background(), partial.Query, func(p award.Progress) {
		if p.Preview != nil {
			previewCounts = append(previewCounts, len(p.Preview.Itineraries))
		}
	})
	if !reflect.DeepEqual(previewCounts, []int{1}) {
		t.Fatalf("recovery erased a useful preview: %v", previewCounts)
	}
}
