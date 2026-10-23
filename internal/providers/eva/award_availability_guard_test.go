package eva

import (
	"strings"
	"testing"
)

func TestAwardAvailabilityNoServiceNoticeDoesNotOverrideGrid(t *testing.T) {
	q := awardQuery()
	base := string(awardFixture(t, "availability.html"))
	want, err := parseAwardAvailability([]byte(base), q)
	if err != nil || len(want) == 0 {
		t.Fatalf("fixture must contain available flights: count=%d err=%v", len(want), err)
	}
	const notice = "The class of service you want to book is not available on this route or we do not have a flight on the day you want to travel."
	for _, hidden := range []bool{false, true} {
		name, attributes := "visible notice", ""
		if hidden {
			name, attributes = "hidden notice", ` hidden style="display:none"`
		}
		t.Run(name, func(t *testing.T) {

			body := strings.Replace(base, `<div id="mainContent">`, `<div id="mainContent"><p`+attributes+`>`+notice+`</p>`, 1)
			if body == base {
				t.Fatal("fixture mainContent marker changed")
			}
			available, err := parseAwardAvailability([]byte(body), q)
			if err != nil || len(available) != len(want) {
				t.Fatalf("notice overrode live grid: available=%v err=%v", available, err)
			}
			for flight := range want {
				if !available[flight] {
					t.Fatalf("notice hid available flight %s", flight)
				}
			}
		})
	}
}
