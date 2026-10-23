package eva

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"awarddesk/internal/award"
)

type AwardQuoteTarget struct {
	Segments []award.Segment
}

type AwardQuoteWorker func(context.Context, award.Query, []AwardQuoteTarget, award.Reporter) (award.Result, error)
type AwardQuoteWorkerFactory func(int) []AwardQuoteWorker
type awardQuoteWorkerFactoryKey struct{}
type awardQuoteTargetsKey struct{}

func WithAwardQuoteWorkerFactory(ctx context.Context, factory AwardQuoteWorkerFactory) context.Context {
	return context.WithValue(ctx, awardQuoteWorkerFactoryKey{}, factory)
}

func (p *Provider) SearchAwardQuotes(ctx context.Context, q award.Query, targets []AwardQuoteTarget, report award.Reporter) (award.Result, error) {
	if q.Mode != "award" || len(targets) == 0 {
		return award.Result{Query: q}, award.Failure("unsupported", "Unsupported award quote worker request")
	}
	items := make([]award.Itinerary, len(targets))
	for i, target := range targets {
		if len(target.Segments) == 0 {
			return award.Result{Query: q}, award.Failure("unsupported", "Award quote worker itinerary is missing")
		}
		items[i].Segments = target.Segments
	}
	snapshot := award.CloneResult(award.Result{Itineraries: items})
	cloned := make([]AwardQuoteTarget, len(targets))
	for i, item := range snapshot.Itineraries {
		cloned[i].Segments = item.Segments
	}
	ctx = context.WithValue(ctx, awardQuoteTargetsKey{}, cloned)
	ctx = context.WithValue(ctx, savedSessionOnlyKey{}, true)
	ctx = WithAwardQuoteWorkerFactory(ctx, nil)
	return p.searchAward(ctx, q, report)
}

func awardWorkerChoices(ctx context.Context, choices []awardChoice) []awardChoice {
	targets, worker := ctx.Value(awardQuoteTargetsKey{}).([]AwardQuoteTarget)
	if !worker {
		return choices
	}
	selected := make([]awardChoice, 0, len(targets))
	for _, choice := range choices {
		for _, target := range targets {
			if sameAwardChoice(choice, awardChoice{itinerary: award.Itinerary{Segments: target.Segments}}) {
				selected = append(selected, choice)
				break
			}
		}
	}
	return selected
}

func orderAwardItineraries(result *award.Result, choices []awardChoice) {
	ordered := make([]award.Itinerary, 0, len(result.Itineraries))
	used := make([]bool, len(result.Itineraries))
	for _, choice := range choices {
		for i, item := range result.Itineraries {
			if !used[i] && sameAwardChoice(choice, awardChoice{itinerary: item}) {
				ordered = append(ordered, item)
				used[i] = true
				break
			}
		}
	}
	for i, item := range result.Itineraries {
		if !used[i] {
			ordered = append(ordered, item)
		}
	}
	result.Itineraries = ordered
}

func priceAwardWithWorkers(ctx context.Context, q award.Query, choices []awardChoice, result *award.Result, price func([]awardChoice) error, report award.Reporter) error {
	factory, _ := ctx.Value(awardQuoteWorkerFactoryKey{}).(AwardQuoteWorkerFactory)
	if factory == nil || len(choices) < 2 {
		return price(choices)
	}
	max := len(choices) - 1
	if max > 2 {
		max = 2
	}
	workers := factory(max)
	if len(workers) > max {
		workers = workers[:max]
	}
	if len(workers) == 0 {
		return price(choices)
	}
	slog.Info("EVA award quotes parallelized", "workers", len(workers)+1, "flights", len(choices), "origin", q.Origin, "destination", q.Destination, "date", q.Date, "cabin", q.Cabin, "party", q.Adults)
	lanes := make([][]awardChoice, len(workers)+1)
	for i, choice := range choices {
		lanes[i%len(lanes)] = append(lanes[i%len(lanes)], choice)
	}
	type completed struct {
		result     award.Result
		assigned   []awardChoice
		err        error
		recoveries []award.Progress
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	finished := make(chan completed, len(workers))
	var joined sync.WaitGroup
	for i, worker := range workers {
		assigned := lanes[i+1]
		items := make([]award.Itinerary, len(assigned))
		for j, choice := range assigned {
			items[j] = choice.itinerary
		}
		isolated := award.CloneResult(award.Result{Itineraries: items})
		targets := make([]AwardQuoteTarget, len(assigned))
		for j, item := range isolated.Itineraries {
			targets[j].Segments = item.Segments
		}
		joined.Add(1)
		go func(worker AwardQuoteWorker, targets []AwardQuoteTarget, assigned []awardChoice) {
			defer joined.Done()
			var r award.Result
			var err error
			var recoveryMu sync.Mutex
			var recoveries []award.Progress
			closed := false
			workerReport := func(pr award.Progress) {
				if pr.Recovery == nil {
					return
				}
				recovery := *pr.Recovery
				recoveryMu.Lock()
				defer recoveryMu.Unlock()
				if !closed {
					recoveries = append(recoveries, award.Progress{Stage: pr.Stage, Recovery: &recovery})
				}
			}
			defer func() {
				if recover() != nil {
					r = award.Result{}
					err = award.Failure("internal", "An independent award quote worker stopped unexpectedly")
				}
				recoveryMu.Lock()
				closed = true
				captured := append([]award.Progress(nil), recoveries...)
				recoveryMu.Unlock()
				finished <- completed{result: r, assigned: assigned, err: err, recoveries: captured}
			}()
			if worker != nil {
				r, err = worker(childCtx, q, targets, workerReport)
			}
		}(worker, targets, assigned)
	}
	defer func() { cancel(); joined.Wait() }()
	awardProgress(report, "award pricing", fmt.Sprintf("Pricing award flights across %d independent accounts", len(lanes)), len(result.Itineraries), len(choices)+len(result.Itineraries), result)
	localErr := price(lanes[0])
	if localErr != nil {
		cancel()
	}
	joined.Wait()
	close(finished)
	assignedHelperQuotes, acceptedHelperQuotes, helperFailures, cancelledHelpers := 0, 0, 0, 0
	for child := range finished {
		assignedHelperQuotes += len(child.assigned)
		accepted := 0
		for _, choice := range child.assigned {
			fare, ok := reusableAwardQuote(award.WithRecoveryResult(ctx, child.result), q, choice, time.Now())
			if !ok {
				continue
			}
			choice.itinerary.Fares = []award.Fare{fare}
			result.Itineraries = append(result.Itineraries, choice.itinerary)
			accepted++
			if child.result.FetchedAt.Before(result.FetchedAt) {
				result.FetchedAt = child.result.FetchedAt
			}
		}
		acceptedHelperQuotes += accepted
		if report != nil {
			for _, pr := range child.recoveries {
				pr.Message = "An independent account recovered before continuing its award quotes"
				pr.Preview = result
				report(pr)
			}
		}
		cleanupCancellation := localErr != nil && errors.Is(child.err, context.Canceled)
		if cleanupCancellation {
			cancelledHelpers++
		}
		if !cleanupCancellation && (child.err != nil || accepted != len(child.assigned)) {
			helperFailures++
		}
		if !cleanupCancellation && (child.err != nil || accepted != len(child.assigned)) && report != nil && ctx.Err() == nil {
			failure := child.err
			if failure == nil {
				failure = award.Failure("award_helper_incomplete", "An independent account did not return every assigned exact award quote")
			}
			report(award.Progress{Stage: "award pricing", Message: "An independent account could not finish its award quotes; retaining verified quotes and continuing missing flights", Recovery: recoveryDetail("award pricing", failure, 1), Preview: result})
		}
	}
	slog.Info("EVA award quote helpers joined", "assignedQuotes", assignedHelperQuotes, "acceptedQuotes", acceptedHelperQuotes, "helperFailures", helperFailures, "cancelledHelpers", cancelledHelpers, "origin", q.Origin, "destination", q.Destination, "date", q.Date, "cabin", q.Cabin, "party", q.Adults)
	if err := ctx.Err(); err != nil {
		return err
	}
	if localErr != nil {
		return localErr
	}
	var missing []awardChoice
	for _, choice := range choices {
		found := false
		for _, item := range result.Itineraries {
			found = found || sameAwardChoice(choice, awardChoice{itinerary: item})
		}
		if !found {
			missing = append(missing, choice)
		}
	}
	if len(missing) > 0 {
		awardProgress(report, "award pricing", "Continuing remaining award quotes on the current account", len(result.Itineraries), len(choices), result)
		return price(missing)
	}
	return nil
}
