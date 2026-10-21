package eva

import (
	"awarddesk/internal/session"
	http "github.com/bogdanfinn/fhttp"
	"testing"
	"time"
)

func TestExpiryUsesMemberCookieWithFiveMinuteMargin(t *testing.T) {
	now := time.Now()
	p := New(Config{SessionMaxAge: 3 * time.Hour})
	s := cached{Created: now, HTTP: session.State{Cookies: []session.CookieEvent{{Cookies: []*http.Cookie{{Name: "EVAID", Expires: now.Add(2 * time.Hour)}, {Name: "incap_ses", Expires: now.Add(30 * time.Second)}, {Name: "sMember", Expires: now.AddDate(1, 0, 0)}}}}}}
	got, source := p.expiry(s)
	if !got.Equal(now.Add(115*time.Minute)) || source != "EVAID cookie deadline" {
		t.Fatalf("expiry=%s source=%s", got, source)
	}
	s.ExpiresAt = now.Add(60 * time.Minute)
	s.ExpirySource = "earlier deadline"
	got, _ = p.expiry(s)
	if !got.Equal(s.ExpiresAt) {
		t.Fatal("persisted deadline extended")
	}
}
func TestExpiryFallbackNotSliding(t *testing.T) {
	created := time.Now().Add(-time.Hour)
	p := New(Config{SessionMaxAge: 3 * time.Hour})
	got, _ := p.expiry(cached{Created: created})
	if !got.Equal(created.Add(175 * time.Minute)) {
		t.Fatal("maximum age is not anchored at login")
	}
}
