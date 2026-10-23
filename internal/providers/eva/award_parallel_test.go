package eva

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"awarddesk/internal/award"
)

func parallelAwardChoices(n int) []awardChoice {
	q := awardQuery()
	items := make([]awardChoice, n)
	for i := range items {
		items[i].itinerary = award.Itinerary{ID: fmt.Sprint(i), Segments: []award.Segment{{Cabin: q.Cabin, FlightNumber: fmt.Sprintf("BR%d", i+1), Origin: q.Origin, Destination: q.Destination, DepartureLocal: q.Date + "T10:00:00", ArrivalLocal: q.Date + "T14:00:00"}}}
	}
	return items
}

func parallelAwardResult(q award.Query, targets []AwardQuoteTarget) award.Result {
	r := award.Result{Query: q, FetchedAt: time.Now().UTC(), Complete: true}
	for _, target := range targets {
		r.Itineraries = append(r.Itineraries, award.Itinerary{Segments: target.Segments, Fares: []award.Fare{{Cabin: q.Cabin, AvailableFor: q.Adults, PricingType: "award", Points: 12345}}})
	}
	return r
}

func TestAwardQuoteWorkersOverlapLocalLaneAndBoundExactAssignments(t *testing.T) {
	q := awardQuery()
	choices := parallelAwardChoices(7)
	r := award.Result{Query: q, FetchedAt: time.Now().UTC()}
	entered := make(chan struct{}, 2)
	localStarted := make(chan struct{})
	var helperCalls atomic.Int32
	var factoryCalls int
	worker := func(ctx context.Context, query award.Query, targets []AwardQuoteTarget, _ award.Reporter) (award.Result, error) {
		helperCalls.Add(1)
		entered <- struct{}{}
		select {
		case <-localStarted:
		case <-ctx.Done():
			return award.Result{}, ctx.Err()
		}
		return parallelAwardResult(query, targets), nil
	}
	ctx := WithAwardQuoteWorkerFactory(context.Background(), func(max int) []AwardQuoteWorker {
		factoryCalls++
		if max != 2 {
			t.Fatalf("helperbound=%d", max)
		}
		return []AwardQuoteWorker{worker, worker}
	})
	local := func(items []awardChoice) error {
		if len(items) != 3 {
			t.Fatalf("unbalanced local lane %d", len(items))
		}
		<-entered
		<-entered
		close(localStarted)
		for _, item := range items {
			item.itinerary.Fares = []award.Fare{{Cabin: q.Cabin, AvailableFor: q.Adults, PricingType: "award", Points: 70000}}
			r.Itineraries = append(r.Itineraries, item.itinerary)
		}
		return nil
	}
	if err := priceAwardWithWorkers(ctx, q, choices, &r, local, nil); err != nil {
		t.Fatal(err)
	}
	if factoryCalls != 1 || helperCalls.Load() != 2 || len(r.Itineraries) != 7 {
		t.Fatalf("factory=%d helpers=%d quotes=%d", factoryCalls, helperCalls.Load(), len(r.Itineraries))
	}
	seen := map[string]bool{}
	for _, item := range r.Itineraries {
		flight := item.Segments[0].FlightNumber
		if seen[flight] {
			t.Fatal("flight quoted twice")
		}
		seen[flight] = true
	}
}

func TestAwardQuoteWorkerPartialFailureFallsBackOnlyMissingExactFlight(t *testing.T) {
	q := awardQuery()
	choices := parallelAwardChoices(4)
	r := award.Result{Query: q, FetchedAt: time.Now().UTC()}
	var batches [][]awardChoice
	recoveries := 0
	worker := func(_ context.Context, query award.Query, targets []AwardQuoteTarget, _ award.Reporter) (award.Result, error) {
		child := parallelAwardResult(query, targets)
		child.Itineraries[1].Segments = append([]award.Segment(nil), child.Itineraries[1].Segments...)
		child.Itineraries[1].Segments[0].ArrivalLocal = "2026-11-24T14:05:00"
		return child, award.Failure("session_denied", "synthetic helper interruption")
	}
	ctx := WithAwardQuoteWorkerFactory(context.Background(), func(int) []AwardQuoteWorker { return []AwardQuoteWorker{worker} })
	local := func(items []awardChoice) error {
		batches = append(batches, items)
		for _, item := range items {
			item.itinerary.Fares = []award.Fare{{Cabin: q.Cabin, AvailableFor: q.Adults, PricingType: "award", Points: 70000}}
			r.Itineraries = append(r.Itineraries, item.itinerary)
		}
		return nil
	}
	if err := priceAwardWithWorkers(ctx, q, choices, &r, local, func(p award.Progress) {
		if p.Recovery != nil {
			recoveries++
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(batches) != 2 || len(batches[0]) != 2 || len(batches[1]) != 1 || batches[1][0].itinerary.Segments[0].FlightNumber != "BR4" || len(r.Itineraries) != 4 || recoveries != 1 {
		t.Fatalf("batches=%+v quotes=%d recoveries=%d", batches, len(r.Itineraries), recoveries)
	}
}

func TestAwardQuoteWorkersJoinOnCancellationAndLocalPanic(t *testing.T) {
	for _, panicLocal := range []bool{false, true} {
		t.Run(fmt.Sprint(panicLocal), func(t *testing.T) {
			q := awardQuery()
			choices := parallelAwardChoices(2)
			r := award.Result{Query: q, FetchedAt: time.Now().UTC()}
			started := make(chan struct{})
			joined := make(chan struct{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = WithAwardQuoteWorkerFactory(ctx, func(int) []AwardQuoteWorker {
				return []AwardQuoteWorker{func(ctx context.Context, _ award.Query, _ []AwardQuoteTarget, _ award.Reporter) (award.Result, error) {
					close(started)
					defer close(joined)
					<-ctx.Done()
					return award.Result{}, ctx.Err()
				}}
			})
			local := func([]awardChoice) error {
				<-started
				if panicLocal {
					panic("synthetic local failure")
				}
				cancel()
				return ctx.Err()
			}
			if panicLocal {
				func() {
					defer func() {
						if recover() == nil {
							t.Fatal("expected local panic")
						}
						select {
						case <-joined:
						default:
							t.Fatal("helper outlived parentpanic")
						}
					}()
					_ = priceAwardWithWorkers(ctx, q, choices, &r, local, nil)
				}()
			} else if err := priceAwardWithWorkers(ctx, q, choices, &r, local, nil); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost %v", err)
			}
			select {
			case <-joined:
			default:
				t.Fatal("helper not joined")
			}
		})
	}
}

func TestAwardLocalFailureDoesNotCountHelperCleanupAsRecovery(t *testing.T) {
	for _, actualHelperFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(actualHelperFailure), func(t *testing.T) {
			q := awardQuery()
			choices := parallelAwardChoices(5)
			r := award.Result{Query: q, FetchedAt: time.Now().UTC()}
			started := make(chan struct{}, 2)
			workerWithPartial := func(ctx context.Context, query award.Query, targets []AwardQuoteTarget, _ award.Reporter) (award.Result, error) {
				started <- struct{}{}
				<-ctx.Done()
				return parallelAwardResult(query, targets[:1]), fmt.Errorf("helper cleanup: %w", ctx.Err())
			}
			otherWorker := func(ctx context.Context, _ award.Query, _ []AwardQuoteTarget, _ award.Reporter) (award.Result, error) {
				started <- struct{}{}
				<-ctx.Done()
				if actualHelperFailure {
					return award.Result{}, award.Failure("session_denied", "independent helper failure")
				}
				return award.Result{}, ctx.Err()
			}
			ctx := WithAwardQuoteWorkerFactory(context.Background(), func(int) []AwardQuoteWorker {
				return []AwardQuoteWorker{workerWithPartial, otherWorker}
			})
			localFailure := award.Failure("award_protocol", "primary account selection changed")
			recoveries := 0
			err := priceAwardWithWorkers(ctx, q, choices, &r, func([]awardChoice) error {
				<-started
				<-started
				return localFailure
			}, func(p award.Progress) {
				if p.Recovery != nil {
					recoveries++
					if p.Recovery.Code != "session_denied" {
						t.Errorf("cleanup cancellation reported as recovery: %+v", p.Recovery)
					}
				}
			})
			wantRecoveries := 0
			if actualHelperFailure {
				wantRecoveries = 1
			}
			if err != localFailure || recoveries != wantRecoveries || len(r.Itineraries) != 1 {
				t.Fatalf("parent failure or valid partial lost; err=%v recoveries=%d partial=%d", err, recoveries, len(r.Itineraries))
			}
		})
	}
}

func TestAwardChildRecoveriesPublishAfterJoinWithOnlyRootPreview(t *testing.T) {
	q := awardQuery()
	choices := parallelAwardChoices(2)
	r := award.Result{Query: q, FetchedAt: time.Now().UTC()}
	reported := make(chan struct{})
	localDone := make(chan struct{})
	localComplete := false
	worker := func(ctx context.Context, query award.Query, targets []AwardQuoteTarget, report award.Reporter) (award.Result, error) {
		childPreview := award.Result{Itineraries: []award.Itinerary{{ID: "unrelated-child-inventory"}}}
		detail := &award.Recovery{Stage: "award flights", Code: "session_denied", Attempt: 1}
		report(award.Progress{Stage: "award availability", Message: "child-only message", Recovery: detail, Preview: &childPreview})
		detail.Code = "changed-after-report"
		report(award.Progress{Stage: "mailbox", Message: "child-only progress", Preview: &childPreview})
		close(reported)
		select {
		case <-localDone:
		case <-ctx.Done():
			return award.Result{}, ctx.Err()
		}
		return parallelAwardResult(query, targets), nil
	}
	ctx := WithAwardQuoteWorkerFactory(context.Background(), func(int) []AwardQuoteWorker { return []AwardQuoteWorker{worker} })
	recoveries := 0
	err := priceAwardWithWorkers(ctx, q, choices, &r, func(items []awardChoice) error {
		<-reported
		for _, choice := range items {
			r.Itineraries = append(r.Itineraries, choice.itinerary)
		}
		localComplete = true
		close(localDone)
		return nil
	}, func(pr award.Progress) {
		if pr.Message == "child-only message" || pr.Message == "child-only progress" || pr.Stage == "mailbox" {
			t.Error("child progress leaked")
		}
		if pr.Recovery == nil {
			return
		}
		recoveries++
		if !localComplete || pr.Stage != "award availability" || pr.Recovery.Stage != "award flights" || pr.Recovery.Code != "session_denied" || pr.Preview != &r || len(pr.Preview.Itineraries) != 2 {
			t.Errorf("unsafe or incomplete child recovery publication: %+v", pr)
		}
		for _, item := range pr.Preview.Itineraries {
			if item.ID == "unrelated-child-inventory" {
				t.Error("child preview leaked")
			}
		}
	})
	if err != nil || recoveries != 1 {
		t.Fatalf("err=%v recoveries=%d", err, recoveries)
	}
}

func TestAwardQuoteWorkersSkipFactoryForOneFlightAndFilterTargets(t *testing.T) {
	q := awardQuery()
	choices := parallelAwardChoices(3)
	r := award.Result{Query: q, FetchedAt: time.Now()}
	ctx := WithAwardQuoteWorkerFactory(context.Background(), func(int) []AwardQuoteWorker { t.Fatal("factory called without parallelwork"); return nil })
	if err := priceAwardWithWorkers(ctx, q, choices[:1], &r, func(items []awardChoice) error {
		if len(items) != 1 {
			t.Fatal("lostlocalwork")
		}
		return nil
	}, nil); err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, awardQuoteTargetsKey{}, []AwardQuoteTarget{{Segments: choices[1].itinerary.Segments}})
	selected := awardWorkerChoices(ctx, choices)
	if len(selected) != 1 || !sameAwardChoice(selected[0], choices[1]) {
		t.Fatal("worker quoted unassigned flights")
	}
	_, _, err := New(Config{}).loginAwardPortal(ctx, nil, "", nil, awardItineraryURL, nil)
	var failure *award.Error
	if !errors.As(err, &failure) || failure.Code != "award_helper_not_ready" {
		t.Fatalf("helper attempted freshlogin: %v", err)
	}
}

func TestAwardQuoteResultsRestoreFreshChoiceOrderAcrossRecoveryAndWorkers(t *testing.T) {
	choices := parallelAwardChoices(5)
	for _, arrival := range [][]int{{3, 0, 2, 4, 1}, {3, 0, 2, 1, 4}} {
		var r award.Result
		for _, index := range arrival {
			item := choices[index].itinerary
			item.Fares = []award.Fare{{Points: 1000 + index, PricingType: "award"}}
			r.Itineraries = append(r.Itineraries, item)
		}
		orderAwardItineraries(&r, choices)
		for i, item := range r.Itineraries {
			if !sameAwardChoice(choices[i], awardChoice{itinerary: item}) || item.Fares[0].Points != 1000+i {
				t.Fatalf("quote order or fare association changed: index=%d item=%+v", i, item)
			}
		}
	}
	partial := award.Result{Itineraries: []award.Itinerary{choices[4].itinerary, choices[1].itinerary}}
	orderAwardItineraries(&partial, choices)
	if len(partial.Itineraries) != 2 || !sameAwardChoice(choices[1], awardChoice{itinerary: partial.Itineraries[0]}) || !sameAwardChoice(choices[4], awardChoice{itinerary: partial.Itineraries[1]}) {
		t.Fatal("ordering invented missing flights or misplaced partial quotes")
	}
}
