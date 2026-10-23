package fleet

import (
	"context"
	"errors"
	"testing"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/providers/eva"
)

func TestAwardHelperBudgetReservesFallbackAndCapsSpeculation(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name                string
		targets, helpers    int
		remaining, expected time.Duration
	}{
		{"one flight", 1, 2, 0, 80 * time.Second},
		{"two flights", 2, 2, 0, 115 * time.Second},
		{"large assignment capped", 8, 2, 0, 3 * time.Minute},
		{"fallback for both helpers", 2, 2, 4 * time.Minute, 70 * time.Second},
		{"last seconds reserved", 2, 2, 3 * time.Minute, 10 * time.Second},
		{"insufficient useful time", 2, 2, 175 * time.Second, 0},
		{"parent already near deadline", 2, 1, time.Second, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			if test.remaining > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, now.Add(test.remaining))
				defer cancel()
			}
			if budget := awardHelperBudget(ctx, test.targets, test.helpers, now); budget != test.expected {
				t.Fatalf("budget=%s expected=%s", budget, test.expected)
			}
		})
	}
}

func TestAwardHelperBudgetSkipMakesNoAirlineRequestAndReleasesLease(t *testing.T) {
	f, p := helperFleet(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	factory, cleanup := f.awardHelperFactory(ctx, map[string]bool{"a": true})
	defer cleanup()
	workers := factory(1)
	if len(workers) != 1 {
		t.Fatal("missing helper")
	}
	_, err := workers[0](ctx, award.Query{Mode: "award"}, []eva.AwardQuoteTarget{{Segments: []award.Segment{{FlightNumber: "BR216"}}}}, nil)
	var failure *award.Error
	if !errors.As(err, &failure) || failure.Code != "award_helper_budget" {
		t.Fatal("missing budget skip classification", err)
	}
	if p["b"].quoteCalls.Load() != 0 {
		t.Fatal("helper consumed airline work despite insufficient fallback reserve")
	}
	if err := f.Edit("b", func() error { return nil }); err != nil {
		t.Fatal("budget skip leaked account lease", err)
	}
}

func TestAwardHelperBudgetHonorsLogicalExecutionDeadline(t *testing.T) {
	now := time.Now()
	ctx := award.WithExecutionDeadlineSource(context.Background(), func() time.Time { return now.Add(4 * time.Minute) })
	if budget := awardHelperBudget(ctx, 2, 2, now); budget != 70*time.Second {
		t.Fatalf("logical execution deadline was ignored: budget=%s", budget)
	}
}

func TestAwardHelperDeadlineOnlyClassifiesSpeculativeTimeout(t *testing.T) {
	worker, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	got := awardHelperResultError(worker, context.Background(), context.DeadlineExceeded)
	var failure *award.Error
	if !errors.As(got, &failure) || failure.Code != "award_helper_timeout" || failure.OutcomeUnknown {
		t.Fatal("speculative timeout not classified", got)
	}
	if got := awardHelperResultError(worker, worker, context.DeadlineExceeded); !errors.Is(got, context.DeadlineExceeded) {
		t.Fatal("parent deadline was hidden", got)
	}
	if got := awardHelperResultError(context.Background(), context.Background(), context.DeadlineExceeded); !errors.Is(got, context.DeadlineExceeded) {
		t.Fatal("unrelated timeout reclassified", got)
	}
	if got := awardHelperResultError(worker, context.Background(), nil); got != nil {
		t.Fatal("completed quote became timeout", got)
	}
}
