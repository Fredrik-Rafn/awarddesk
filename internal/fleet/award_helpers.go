package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/providers/eva"
	"awarddesk/internal/storage"
)

type awardQuoteProvider interface {
	SearchAwardQuotes(context.Context, award.Query, []eva.AwardQuoteTarget, award.Reporter) (award.Result, error)
}

type awardHelper struct {
	account  storage.Account
	provider airline
	quotes   awardQuoteProvider
	release  func()
}

func awardHelperBudget(ctx context.Context, targets, helpers int, now time.Time) time.Duration {
	budget := 45*time.Second + time.Duration(targets)*35*time.Second
	if budget > 3*time.Minute {
		budget = 3 * time.Minute
	}
	if deadline, ok := award.ExecutionDeadline(ctx); ok {
		reserve := 30*time.Second + time.Duration(targets*helpers)*35*time.Second
		if available := deadline.Sub(now) - reserve; available < budget {
			budget = available
		}
	}
	if budget < 10*time.Second {
		return 0
	}
	return budget
}

func awardHelperResultError(worker, parent context.Context, err error) error {
	if err != nil && errors.Is(worker.Err(), context.DeadlineExceeded) && parent.Err() == nil {
		return &award.Error{Code: "award_helper_timeout", Stage: "award pricing", Message: "Award quotation helper reached its time budget; continuing missing quotes on the current account"}
	}
	return err
}

func (f *Fleet) claimAwardHelpers(ctx context.Context, excluded map[string]bool, maximum int) []awardHelper {
	if ctx.Err() != nil || maximum < 1 {
		return nil
	}
	if maximum > 2 {
		maximum = 2
	}
	accounts, err := f.db.Accounts()
	if err != nil {
		return nil
	}
	settings, err := f.db.RuntimeSettings()
	if err != nil {
		return nil
	}
	runtime, err := f.db.AccountRuntime()
	if err != nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || ctx.Err() != nil {
		return nil
	}
	remaining := award.ConcurrencyLimit(settings["SEARCH_CONCURRENCY"]) - f.registrations
	for _, entry := range f.entries {
		if entry.busy {
			remaining--
		}
	}
	if remaining < maximum {
		maximum = remaining
	}
	if maximum < 1 {
		return nil
	}
	type candidate struct {
		account storage.Account
		entry   *entry
		quotes  awardQuoteProvider
	}
	var candidates []candidate
	for _, account := range accounts {
		if !configured(account) || excluded[account.ID] || f.pending[account.ID] {
			continue
		}
		status := Status{}
		if raw := runtime["account:"+account.ID]; raw != nil {
			_ = json.Unmarshal(raw, &status)
		}
		if status.NeedsAttention || time.Now().Before(status.NextAttempt) {
			continue
		}
		entry := f.providerLocked(account, settings)
		quotes, ok := entry.provider.(awardQuoteProvider)
		if !ok || entry.busy || !entry.provider.SessionInfo().Ready {
			continue
		}
		candidates = append(candidates, candidate{account, entry, quotes})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].entry.last.Before(candidates[j].entry.last) })
	var helpers []awardHelper
	for _, candidate := range candidates {
		if len(helpers) >= maximum || ctx.Err() != nil {
			break
		}
		unlock, err := f.db.LockState("account:" + candidate.account.ID)
		if err != nil {
			continue
		}
		entry := candidate.entry
		entry.busy = true
		var once sync.Once
		release := func() {
			once.Do(func() {
				unlock()
				f.mu.Lock()
				entry.busy, entry.last = false, time.Now()
				f.notifyLocked()
				f.mu.Unlock()
			})
		}
		helpers = append(helpers, awardHelper{candidate.account, entry.provider, candidate.quotes, release})
	}
	return helpers
}

func (f *Fleet) awardHelperFactory(ctx context.Context, excluded map[string]bool) (eva.AwardQuoteWorkerFactory, func()) {
	ctx, cancel := context.WithCancel(ctx)
	var mu sync.Mutex
	var closed, acquired bool
	var helpers []awardHelper
	var running sync.WaitGroup
	factory := func(maximum int) []eva.AwardQuoteWorker {
		mu.Lock()
		defer mu.Unlock()
		if closed || acquired || ctx.Err() != nil {
			return nil
		}
		acquired = true
		helpers = f.claimAwardHelpers(ctx, excluded, maximum)
		workers := make([]eva.AwardQuoteWorker, 0, len(helpers))
		for _, helper := range helpers {
			used := false
			workers = append(workers, func(callCtx context.Context, q award.Query, targets []eva.AwardQuoteTarget, report award.Reporter) (result award.Result, err error) {
				mu.Lock()
				if closed || used || ctx.Err() != nil || callCtx.Err() != nil {
					mu.Unlock()
					return award.Result{Query: q}, context.Canceled
				}
				used = true
				running.Add(1)
				mu.Unlock()
				defer running.Done()
				defer helper.release()
				budget := awardHelperBudget(callCtx, len(targets), len(helpers), time.Now())
				if budget == 0 {
					return award.Result{Query: q}, &award.Error{Code: "award_helper_budget", Stage: "award pricing", Message: "Keeping remaining search time for quotation on the current account"}
				}
				parentCtx := callCtx
				callCtx, stop := context.WithTimeout(callCtx, budget)
				stopParent := context.AfterFunc(ctx, stop)
				defer stopParent()
				defer stop()
				defer func() {
					if recover() != nil {
						err = &award.Error{Code: "internal", Stage: "award pricing", Message: "EVA quotation helper stopped unexpectedly", OutcomeUnknown: true}
					}
					err = awardHelperResultError(callCtx, parentCtx, err)
					outcomeErr := err
					var failure *award.Error
					if errors.As(err, &failure) && (failure.Code == "award_helper_not_ready" || failure.Code == "award_helper_timeout") {
						outcomeErr = nil
					}
					f.outcome(helper.account.ID, helper.provider, outcomeErr, "award")
				}()
				status := Status{ID: helper.account.ID}
				f.db.RuntimeValue("account:"+helper.account.ID, &status)
				status.State, status.CheckedAt = "searching", time.Now().UTC()
				return helper.quotes.SearchAwardQuotes(callCtx, q, targets, func(progress award.Progress) {
					status.Stage, status.Message = progress.Stage, progress.Message
					_ = f.db.Runtime("account:"+helper.account.ID, status)
					if report != nil {
						report(progress)
					}
				})
			})
		}
		return workers
	}
	cleanup := func() {
		cancel()
		mu.Lock()
		closed = true
		reserved := append([]awardHelper(nil), helpers...)
		mu.Unlock()
		running.Wait()
		for _, helper := range reserved {
			helper.release()
		}
	}
	return factory, cleanup
}
