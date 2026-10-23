package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/jobs"
)

type jobProvider struct {
	run func(context.Context) (award.Result, error)
}

func (p jobProvider) Capabilities() award.Capabilities {
	return award.Capabilities{ID: "test", Cabins: []award.Cabin{award.Economy}, MaxAdults: 9}
}
func (p jobProvider) Search(ctx context.Context, _ award.Query, _ award.Reporter) (award.Result, error) {
	return p.run(ctx)
}
func jobQuery() award.Query {
	return award.Query{Provider: "test", Origin: "JFK", Destination: "LAX", Date: time.Now().AddDate(0, 0, 1).Format("2006-01-02"), Cabin: award.Economy, Adults: 1}
}
func uniqueJobQuery(i int) award.Query {
	q := jobQuery()
	q.Origin = fmt.Sprintf("%c%c%c", 'A'+rune(i/676%26), 'A'+rune(i/26%26), 'A'+rune(i%26))
	if q.Origin == q.Destination {
		q.Destination = "SFO"
	}
	return q
}
func awaitJob(t *testing.T, s *jobs.Service, id, status string) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := s.Get(id)
		if j.Status == status {
			return j
		}
		time.Sleep(time.Millisecond)
	}
	j, _ := s.Get(id)
	t.Fatalf("expected %s, got %s", status, j.Status)
	return jobs.Job{}
}
