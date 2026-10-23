package award

import (
	"context"
	"testing"
	"time"
)

func TestExecutionDeadlineStartsAfterCapacityAndRespectsEarlierContext(t *testing.T) {
	var started time.Time
	ctx := WithExecutionDeadlineSource(context.Background(), func() time.Time { return started })
	if _, ok := ExecutionDeadline(ctx); ok {
		t.Fatal("queue waiting consumed an execution deadline")
	}
	started = time.Now().Add(15 * time.Minute)
	if got, ok := ExecutionDeadline(ctx); !ok || !got.Equal(started) {
		t.Fatal("execution deadline is missing after account acquisition")
	}
	earlier := time.Now().Add(time.Minute)
	bounded, cancel := context.WithDeadline(ctx, earlier)
	defer cancel()
	if got, ok := ExecutionDeadline(bounded); !ok || !got.Equal(earlier) {
		t.Fatal("logical budget extended a caller deadline")
	}
	later, stop := context.WithDeadline(ctx, started.Add(time.Hour))
	defer stop()
	if got, ok := ExecutionDeadline(later); !ok || !got.Equal(started) {
		t.Fatal("caller deadline extended the execution budget")
	}
}
