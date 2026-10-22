package ba

import (
	"context"
	"errors"
	"testing"
	"time"

	"awarddesk/internal/award"
)

func TestSearchWaitingForAccountHonorsCancellation(t *testing.T) {
	p := New(Config{})
	p.gate <- struct{}{} // Another search owns the account.
	defer func() { <-p.gate }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	q := testQuery()
	result, err := p.Search(ctx, q, nil)
	if !errors.Is(err, context.DeadlineExceeded) || result.Query != q {
		t.Fatalf("waiting search: query=%+v error=%v", result.Query, err)
	}
	if len(p.gate) != 1 {
		t.Fatal("waiting search released another search's account")
	}
}

func TestSearchReleasesAccountOnConfigurationFailure(t *testing.T) {
	p := New(Config{})
	_, err := p.Search(context.Background(), testQuery(), nil)
	var failure *award.Error
	if !errors.As(err, &failure) || failure.Code != "configuration" {
		t.Fatalf("unexpected failure: %v", err)
	}
	if len(p.gate) != 0 {
		t.Fatal("failed search retained the account")
	}
}

func testQuery() award.Query {
	return award.Query{Provider: "ba", Origin: "JFK", Destination: "LAX", Date: time.Now().AddDate(0, 0, 1).Format("2006-01-02"), Cabin: award.Economy, Adults: 1}
}
