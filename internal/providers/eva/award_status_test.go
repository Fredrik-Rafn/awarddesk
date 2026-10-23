package eva

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"awarddesk/internal/award"
)

func TestAwardBookingUnknownStatusCannotSilentlyHideAnAvailableFlight(t *testing.T) {
	q := awardQuery()
	body := strings.ReplaceAll(string(awardFixture(t, "journeys.html")), ">waiting<", ">Unrecognized status<")
	_, err := parseAwardChoices([]byte(body), q, map[string]bool{"BR216": true, "BR226": true})
	var failure *award.Error
	if !errors.As(err, &failure) || failure.Code != "award_protocol" {
		t.Fatalf("unrecognized booking status was silently discarded: %v", err)
	}
}

func TestAwardStatusImageDiagnosticExcludesURLState(t *testing.T) {
	for _, tt := range []struct{ source, want string }{
		{"https://user:private@example.com/private-directory/new-status.png?session=private#private", "new-status.png"},
		{"../icons/Triangle.png?opaque=private", "Triangle.png"},
		{"BR.png", "BR.png"},
		{"https://example.com/private-directory/", ""},
		{"https://example.com?session=private", ""},
		{"https://example.com/%zz?session=private", ""},
		{"https://example.com/" + strings.Repeat("a", 100) + ".png?opaque=private", strings.Repeat("a", 64)},
	} {
		if got := awardStatusImageFilename(tt.source); got != tt.want {
			t.Errorf("filename diagnostic=%q, want %q", got, tt.want)
		}
	}
}

func TestAwardStatusLabelDiagnosticIsBoundedAndSingleLine(t *testing.T) {
	label := awardStatusLabel("\n New\r\nstatus\t\x00 ")
	if label != "Newstatus" {
		t.Fatalf("unsafe diagnostic label: %q", label)
	}
	label = awardStatusLabel(strings.Repeat("界", 100))
	if !utf8.ValidString(label) || utf8.RuneCountInString(label) != 64 {
		t.Fatal("status label diagnostic is unbounded or breaks Unicode")
	}
}

func TestAwardAvailabilityKnownStatusVariants(t *testing.T) {
	for _, tt := range []struct {
		label     string
		available bool
	}{
		{"Available", true}, {" available ", true}, {"Waiting", false},
		{"Ｗaiting", false}, {"Unavailable", false}, {"Not Available", false},
	} {
		t.Run(tt.label, func(t *testing.T) {
			q, body := awardBlankStatusFixture(`<img class="icon icon-flightCompany table-flightCompany" src="status.png" alt="` + tt.label + `">`)
			available, err := parseAwardAvailability(body, q)
			if err != nil || available["BR869"] != tt.available {
				t.Fatalf("known status misclassified: available=%v err=%v", available["BR869"], err)
			}
		})
	}
}
