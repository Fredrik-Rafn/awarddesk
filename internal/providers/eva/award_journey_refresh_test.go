package eva

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestAwardJourneyRefreshStopsReopeningDiscardedSearchAndRebuildsFresh(t *testing.T) {
	p := New(Config{})
	p.awardAirports = &awardAirportDirectory{Airports: []awardAirport{{Code: "SIN", Continent: "AS", Community: "03"}, {Code: "TPE", Continent: "AS", Community: "01"}}}
	gets, rebuilds := 0, 0
	nav := func(_ context.Context, method, target string, values url.Values, _ string) ([]byte, string, error) {
		switch {
		case method == "GET" && target == awardJourneyURL:
			gets++
			return []byte(`<form>No remaining search</form>`), target, nil
		case method == "GET" && target == awardItineraryURL:
			rebuilds++
			return awardFixture(t, "itinerary.html"), target, nil
		case strings.HasPrefix(target, awardItineraryURL):
			return []byte(`<form action="select-first-journey.aspx"><input name="Parameter" value="fresh"></form>`), target, nil
		case target == awardJourneyURL && values.Has("Parameter"):
			return []byte(strings.ReplaceAll(string(awardFixture(t, "journeys.html")), "fresh-test-state", fmt.Sprintf("new-state-%d", rebuilds))), target, nil
		default:
			t.Fatalf("unexpected %s %s", method, target)
			return nil, target, nil
		}
	}
	rebuild := false
	for i := 1; i <= 2; i++ {
		body, target, choices, err := p.refreshAwardChoices(context.Background(), awardQuery(), nav, map[string]bool{"BR216": true}, awardJourneyURL, &rebuild)
		if err != nil || target != awardJourneyURL || len(choices) != 1 || !strings.Contains(string(body), fmt.Sprintf("new-state-%d", i)) {
			t.Fatalf("fresh rebuild failed: choices=%d err=%v", len(choices), err)
		}
	}
	if gets != 1 || rebuilds != 2 || !rebuild {
		t.Fatalf("doomedgets=%d freshrebuilds=%d mode=%v", gets, rebuilds, rebuild)
	}
}
