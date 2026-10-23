package eva

import (
	"errors"
	"strings"
	"testing"

	"awarddesk/internal/award"
)

func TestAwardContinuationKeepsAuthenticationUncertaintySeparate(t *testing.T) {
	for _, page := range []string{awardAvailabilityURL, awardItineraryURL, awardJourneyURL, "https://booking.evaair.com" + awardTicketPath + "mileage-deducted.aspx", "https://booking.evaair.com" + awardTicketPath + "login.aspx"} {
		for _, code := range []string{"connection", "response_read"} {
			var failure *award.Error
			err := loginStageError(&award.Error{Code: code}, awardContinuationStage(page))
			if !errors.As(err, &failure) || failure.OutcomeUnknown != awardLoginPage(page) {
				t.Fatalf("wrong continuation uncertainty for %s: %v", page, err)
			}
		}
	}
}

func TestAwardForwardResponseAllowsOnlyCurrentAwardWrapper(t *testing.T) {
	wrapper := func(action, payload, contentType string) []byte {
		return []byte(`<form action="` + action + `"><input name="sec_chlge_forward_wrap" value="` + payload + `"><input name="sec_chlge_content_type_wrap" value="` + contentType + `"></form>`)
	}
	for _, target := range []string{
		awardAvailabilityURL,
		awardItineraryURL,
		awardJourneyURL,
		"https://booking.evaair.com" + awardTicketPath + "mileage-deducted.aspx",
		"https://booking.evaair.com" + awardTicketPath + "login.aspx",
		"https://booking.evaair.com/flyeva/eva/b2c/plan-your-journey/award-upgrade-availability/login.aspx",
	} {
		for _, status := range []int{403, 428} {
			if !awardForwardResponse(status, target, wrapper(target+"?akamai-challenge-resubmit=true", "synthetic-wrapper", "synthetic-content-type")) {
				t.Errorf("current award wrapper rejected: status=%d page=%s", status, target)
			}
		}
	}

	tests := []struct {
		name   string
		status int
		target string
		body   []byte
	}{
		{"server error", 500, awardJourneyURL, wrapper(awardJourneyURL+"?akamai-challenge-resubmit=true", "synthetic", "synthetic")},
		{"ordinary forbidden", 403, awardJourneyURL, []byte(`<html>Access Denied</html>`)},
		{"missing payload", 403, awardJourneyURL, wrapper(awardJourneyURL+"?akamai-challenge-resubmit=true", "", "synthetic")},
		{"missing content type", 428, awardJourneyURL, wrapper(awardJourneyURL+"?akamai-challenge-resubmit=true", "synthetic", "")},
		{"missing resubmit flag", 403, awardJourneyURL, wrapper(awardJourneyURL, "synthetic", "synthetic")},
		{"false resubmit flag", 403, awardJourneyURL, wrapper(awardJourneyURL+"?akamai-challenge-resubmit=false", "synthetic", "synthetic")},
		{"different award page", 403, awardJourneyURL, wrapper(awardItineraryURL+"?akamai-challenge-resubmit=true", "synthetic", "synthetic")},
		{"passenger page", 403, "https://booking.evaair.com" + awardTicketPath + "passenger-information.aspx", wrapper("?akamai-challenge-resubmit=true", "synthetic", "synthetic")},
		{"foreign target host", 403, strings.Replace(awardJourneyURL, "booking.evaair.com", "example.com", 1), wrapper("?akamai-challenge-resubmit=true", "synthetic", "synthetic")},
		{"cross origin wrapper", 403, awardJourneyURL, wrapper(strings.Replace(awardJourneyURL, "booking.evaair.com", "example.com", 1)+"?akamai-challenge-resubmit=true", "synthetic", "synthetic")},
		{"insecure wrapper", 403, awardJourneyURL, wrapper(strings.Replace(awardJourneyURL, "https://", "http://", 1)+"?akamai-challenge-resubmit=true", "synthetic", "synthetic")},
		{"userinfo wrapper", 403, awardJourneyURL, wrapper(strings.Replace(awardJourneyURL, "https://", "https://user@", 1)+"?akamai-challenge-resubmit=true", "synthetic", "synthetic")},
		{"Miles and Cash", 403, BookingURL, wrapper("?akamai-challenge-resubmit=true", "synthetic", "synthetic")},
		{"Miles and Cash precondition", 428, BookingURL, wrapper("?akamai-challenge-resubmit=true", "synthetic", "synthetic")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if awardForwardResponse(tt.status, tt.target, tt.body) {
				t.Fatal("unrelated or invalid challenge exposed to award continuation")
			}
		})
	}
}
