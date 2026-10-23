package jobs

import (
	"context"
	"testing"
	"time"

	"awarddesk/internal/award"
)

type deadlineObservation struct {
	before          bool
	after, repeated time.Time
}
type deadlineProvider struct{ observations chan deadlineObservation }

func (p deadlineProvider) Capabilities() award.Capabilities { return fakeProvider{}.Capabilities() }
func (p deadlineProvider) DefersExecutionStart() bool       { return true }
func (p deadlineProvider) Search(ctx context.Context, q award.Query, _ award.Reporter) (award.Result, error) {
	_, before := award.ExecutionDeadline(ctx)
	award.ExecutionStarted(ctx)
	after, _ := award.ExecutionDeadline(ctx)
	award.ExecutionStarted(ctx)
	repeated, _ := award.ExecutionDeadline(ctx)
	p.observations <- deadlineObservation{before, after, repeated}
	return award.Result{Query: q, Complete: true}, nil
}

func TestProviderSeesExecutionDeadlineOnlyAfterFirstAccountAcquisition(t *testing.T) {
	p := deadlineProvider{make(chan deadlineObservation, 1)}
	s := New(p)
	defer s.Close()
	s.runBudget = time.Minute
	j, err := s.Submit(query())
	if err != nil {
		t.Fatal(err)
	}
	finished := await(t, s, j.ID, "completed")
	observed := <-p.observations
	if observed.before || finished.StartedAt == nil || !observed.after.Equal(finished.StartedAt.Add(time.Minute)) || !observed.repeated.Equal(observed.after) {
		t.Fatal("execution budget is missing, includes queue waiting, or was extended on another account")
	}
}
