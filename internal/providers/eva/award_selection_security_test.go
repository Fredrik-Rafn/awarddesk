package eva

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestAwardSelectionSecurityUsesFreshPageForEveryActualSelection(t *testing.T) {
	ctx := context.Background()
	securityCalls, posts := 0, 0
	state := "first-state"
	pageBody := func() []byte {
		return []byte(`<form action="select-first-journey.aspx"><input name="ctl00$choice$hiddenSelectValue" value="` + state + `"></form>`)
	}
	nav := withAwardSelectionSecurity(func(_ context.Context, method, target string, values url.Values, from string) ([]byte, string, error) {
		if method == "GET" {
			return pageBody(), target, nil
		}
		posts++
		if securityCalls != posts {
			t.Fatal("protected POST preceded its fresh security preparation")
		}
		return []byte(`<form action="mileage-deducted.aspx"><input name="Parameter" value="fresh"></form>`), target, nil
	}, func(_ context.Context, page string, body []byte, rounds int) error {
		securityCalls++
		if page != awardJourneyURL || string(body) != string(pageBody()) || rounds != 3 {
			t.Fatal("wrong selection security context")
		}
		return nil
	})
	confirm := url.Values{"ctl00$__MasterEVENTTARGET": {"btn_Confirm_Click"}}
	for _, nextState := range []string{"first-state", "second-state"} {
		state = nextState
		if _, _, err := nav(ctx, "GET", awardJourneyURL, nil, ""); err != nil {
			t.Fatal(err)
		}
		if securityCalls != posts {
			t.Fatal("displaying selection spent sensor requests")
		}
		if _, _, err := nav(ctx, "POST", awardJourneyURL, confirm, awardJourneyURL); err != nil {
			t.Fatal(err)
		}
	}
	if securityCalls != 2 || posts != 2 {
		t.Fatalf("preparations=%d posts=%d", securityCalls, posts)
	}
}

func TestAwardSelectionSecurityInvalidPageOrPreparationFailureNeverPosts(t *testing.T) {
	for _, tt := range []struct {
		name, body   string
		fail         bool
		wantSecurity int
	}{
		{"invalid page", `<form>No selection state</form>`, false, 0},
		{"preparation failed", `<form action="select-first-journey.aspx"><input name="ctl00$choice$hiddenSelectValue" value="fresh"></form>`, true, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			securityCalls, posts := 0, 0
			nav := withAwardSelectionSecurity(func(_ context.Context, method, target string, _ url.Values, _ string) ([]byte, string, error) {
				if method == "POST" {
					posts++
				}
				return []byte(tt.body), target, nil
			}, func(context.Context, string, []byte, int) error {
				securityCalls++
				if tt.fail {
					return errors.New("synthetic security failure")
				}
				return nil
			})
			if _, _, err := nav(context.Background(), "GET", awardJourneyURL, nil, ""); err != nil {
				t.Fatal(err)
			}
			confirm := url.Values{"ctl00$__MasterEVENTTARGET": {"btn_Confirm_Click"}}
			if _, _, err := nav(context.Background(), "POST", awardJourneyURL, confirm, awardJourneyURL); err == nil {
				t.Fatal("invalid security context accepted")
			}
			if securityCalls != tt.wantSecurity || posts != 0 {
				t.Fatalf("preparations=%d posts=%d", securityCalls, posts)
			}
			if _, _, err := nav(context.Background(), "POST", awardJourneyURL, confirm, awardJourneyURL); err == nil {
				t.Fatal("failed preparation state replayed")
			}
			if securityCalls != tt.wantSecurity {
				t.Fatal("failed preparation repeated without fresh page")
			}
		})
	}
}

func TestAwardInvalidChoicesDoNotSpendSelectionSensors(t *testing.T) {
	ctx := context.Background()
	q := awardQuery()
	securityCalls := 0
	nav := withAwardSelectionSecurity(func(_ context.Context, method, target string, values url.Values, _ string) ([]byte, string, error) {
		switch {
		case target == awardAvailabilityURL && method == "GET":
			return awardFixture(t, "availability-form.html"), target, nil
		case target == awardAvailabilityURL:
			return awardFixture(t, "availability.html"), target, nil
		case target == awardAirportURL:
			return awardFixture(t, "airports.xml"), target, nil
		case target == awardItineraryURL && method == "GET":
			return awardFixture(t, "itinerary.html"), target, nil
		case strings.HasPrefix(target, awardItineraryURL):
			return []byte(`<form action="select-first-journey.aspx"><input name="Parameter" value="fresh"></form>`), target, nil
		case target == awardJourneyURL && values.Has("Parameter"):
			return []byte(`<form action="select-first-journey.aspx"><input name="ctl00$choice$hiddenSelectValue" value="fresh"><p>No flight cards</p></form>`), target, nil
		default:
			t.Fatal("unexpected request after invalid choices")
			return nil, target, nil
		}
	}, func(context.Context, string, []byte, int) error { securityCalls++; return nil })
	if _, err := New(Config{}).runAwardSearch(ctx, q, nav, nil); err == nil {
		t.Fatal("invalid choices accepted")
	}
	if securityCalls != 0 {
		t.Fatalf("invalid choice page spent %d preparations", securityCalls)
	}
}
