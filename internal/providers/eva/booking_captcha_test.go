package eva

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAdaptiveAvailabilityDetectionIsRestrictedToBookingForm(t *testing.T) {
	body := []byte(`<title>Challenge Validation</title><iframe id="sec-cpt-if" provider="adaptive" data-duration=30></iframe>`)
	if !isAdaptiveBookingChallenge(body, BookingURL) {
		t.Fatal("availability challenge not detected")
	}
	for _, target := range []string{overrideURL, "https://other.test/flyeva/eva/b2c/booking-online.aspx", "http://booking.evaair.com/flyeva/eva/b2c/booking-online.aspx"} {
		if isAdaptiveBookingChallenge(body, target) {
			t.Fatal("challenge accepted outside the search form")
		}
	}
	p := New(Config{})
	if err := p.clearAvailabilityChallenge(context.Background(), nil, body, BookingURL, 1, func(string, string) {}); err == nil {
		t.Fatal("repeated challenge was not bounded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.clearAvailabilityChallenge(ctx, nil, body, BookingURL, 0, func(string, string) {}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled challenge was processed")
	}
}

const bookingCaptchaFixture = `<title>EVAAir</title><div id="imperva-title">book.evaair.com Additional security check is required</div><form id="reese84-resubmit-form" method="POST"><input name="reese84-resubmit-data" value="fresh-wrapper"></form><script>var msg = "g-recaptcha-response=" + response; xhr.open("POST", "/_Incapsula_Resource?SWCGHOEL=v2&dai=fresh-id&cts=fresh-time", true);</script><div class="h-captcha" data-sitekey="e94865c2-4231-4c25-9c6e-2b797b2b56cf"></div>`

func TestBookingCaptchaRecognizesAlternateImpervaPage(t *testing.T) {
	if !isReeseContinuation([]byte(bookingCaptchaFixture), overrideURL) {
		t.Fatal("alternate title hid the continuation")
	}
	key, endpoint, found, err := bookingCaptcha([]byte(bookingCaptchaFixture), overrideURL)
	if err != nil || !found || key != "e94865c2-4231-4c25-9c6e-2b797b2b56cf" || endpoint != "https://book.evaair.com/_Incapsula_Resource?SWCGHOEL=v2&dai=fresh-id&cts=fresh-time" {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

func TestBookingCaptchaRejectsChangedOrExternalVerification(t *testing.T) {
	for _, change := range []struct{ old, replacement string }{
		{"/_Incapsula_Resource?", "https://other.test/_Incapsula_Resource?"},
		{"/_Incapsula_Resource?", "https://user@book.evaair.com/_Incapsula_Resource?"},
		{"SWCGHOEL=v2", "SWCGHOEL=v3"},
		{"&cts=fresh-time", ""},
		{`data-sitekey="e94865c2-4231-4c25-9c6e-2b797b2b56cf"`, `data-sitekey=""`},
		{"g-recaptcha-response=", "changed-response="},
	} {
		_, _, found, err := bookingCaptcha([]byte(strings.ReplaceAll(bookingCaptchaFixture, change.old, change.replacement)), overrideURL)
		if err == nil || !found {
			t.Fatalf("unsafe or changed challenge accepted: %s", change.old)
		}
	}
	if _, _, found, err := bookingCaptcha([]byte("application"), overrideURL); found || err != nil {
		t.Fatal("ordinary page treated as CAPTCHA")
	}
}
