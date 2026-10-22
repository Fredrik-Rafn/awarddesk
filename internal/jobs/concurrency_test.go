package jobs

import (
	"awarddesk/internal/award"
	"context"
	"testing"
	"time"
)

func TestSearchDispatchCanGrowToTwoHundredConcurrentFlows(t *testing.T) {
	entered := make(chan struct{}, 200)
	release := make(chan struct{})
	s := NewConcurrent(1, fakeProvider{run: func(ctx context.Context) (award.Result, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return award.Result{Complete: true}, nil
		case <-ctx.Done():
			return award.Result{}, ctx.Err()
		}
	}})
	defer s.Close()
	s.EnsureWorkers(200)
	for i := 0; i < 200; i++ {
		q := query()
		q.Date = time.Now().AddDate(0, 0, i+1).Format("2006-01-02")
		if _, e := s.Submit(q); e != nil {
			t.Fatal(e)
		}
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for i := 0; i < 200; i++ {
		select {
		case <-entered:
		case <-timer.C:
			t.Fatalf("only %d flows ran concurrently", i)
		}
	}
	close(release)
}
