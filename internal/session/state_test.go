package session

import (
	http "github.com/bogdanfinn/fhttp"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestCookieStateRestoresScopeExpiryAndDeletion(t *testing.T) {
	c, err := New("", []string{"www.example.test", "api.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	u, _ := url.Parse("https://www.example.test/auth/login")
	c.SetCookies(u, []*http.Cookie{
		{Name: "host", Value: "only", Path: "/", Secure: true},
		{Name: "domain", Value: "shared", Domain: ".example.test", Path: "/", Secure: true},
		{Name: "path", Value: "auth-only", Path: "/auth", Secure: true},
		{Name: "expired", Value: "no", Path: "/", Expires: time.Now().Add(-time.Second)},
		{Name: "deleted", Value: "old", Path: "/"},
		{Name: "relative", Value: "value", Path: "/", MaxAge: 60},
	})
	c.SetCookies(u, []*http.Cookie{{Name: "deleted", Value: "", Path: "/", MaxAge: -1}})
	state, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	foundRelative := false
	for _, event := range state.Cookies {
		for _, cookie := range event.Cookies {
			if cookie.Name == "relative" {
				foundRelative = true
				if cookie.MaxAge != 0 || cookie.Expires.IsZero() {
					t.Fatal("relative expiry not made absolute")
				}
			}
		}
	}
	if !foundRelative {
		t.Fatal("live relative-expiry cookie lost")
	}
	restored, err := New("", []string{"www.example.test", "api.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err = restored.Restore(state); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		raw        string
		host, path bool
	}{
		{"https://www.example.test/auth/check", true, true},
		{"https://www.example.test/", true, false},
		{"https://api.example.test/", false, false},
	} {
		target, _ := url.Parse(tc.raw)
		values := map[string]string{}
		for _, cookie := range restored.Cookies(target) {
			values[cookie.Name] = cookie.Value
		}
		if (values["host"] != "") != tc.host || (values["path"] != "") != tc.path || values["domain"] != "shared" || values["expired"] != "" || values["deleted"] != "" {
			t.Fatalf("cookie scope changed at %s: %v", tc.raw, values)
		}
	}
	state.Cookies[0].URL = "https://untrusted.test/"
	other, _ := New("", []string{"www.example.test"})
	defer other.Close()
	if other.Restore(state) == nil {
		t.Fatal("untrusted saved origin accepted")
	}
}

func TestCookieSnapshotsStayBoundedAndPreserveOrder(t *testing.T) {
	c, err := New("", []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	u, _ := url.Parse("https://example.test/auth/login")
	c.SetCookies(u, []*http.Cookie{{Name: "first", Value: "old"}, {Name: "second", Value: "kept"}})
	for n := 0; n < 5000; n++ {
		c.SetCookies(u, []*http.Cookie{{Name: "first", Value: strconv.Itoa(n)}})
	}
	c.SetCookies(u, []*http.Cookie{{Name: "removed", Value: "old"}})
	c.SetCookies(u, []*http.Cookie{{Name: "removed", MaxAge: -1}})
	s, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Cookies) != 2 {
		t.Fatalf("snapshot retained cookie history: %d events", len(s.Cookies))
	}
	r, err := New("", []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err = r.Restore(s); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"https://example.test/auth/page", "https://example.test/"} {
		target, _ := url.Parse(path)
		before, after := c.Cookies(target), r.Cookies(target)
		if len(before) != len(after) {
			t.Fatal("default cookie path changed")
		}
		for n := range before {
			if before[n].Name != after[n].Name || before[n].Value != after[n].Value {
				t.Fatal("live cookie value or header order changed")
			}
		}
	}
}

func TestCompactionRetainsCreationOrderAcrossExpiryRenewal(t *testing.T) {
	now := time.Now()
	u := "https://example.test/"
	events := []CookieEvent{
		{URL: u, At: now.Add(-10 * time.Second), Cookies: []*http.Cookie{{Name: "a", Value: "old", Expires: now.Add(-2 * time.Second)}}},
		{URL: u, At: now.Add(-8 * time.Second), Cookies: []*http.Cookie{{Name: "b", Value: "other"}}},
		{URL: u, At: now.Add(-5 * time.Second), Cookies: []*http.Cookie{{Name: "a", Value: "renewed", Expires: now.Add(time.Hour)}}},
	}
	got := compactCookieEvents(events, now)
	if len(got) != 2 || got[0].Cookies[0].Name != "a" || got[0].Cookies[0].Value != "renewed" {
		t.Fatal("renewed cookie creation order was lost")
	}
}
