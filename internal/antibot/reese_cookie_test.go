package antibot

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"testing"
	"time"

	http "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/cookiejar"
)

type reeseCookieClient struct {
	response string
	set      []*http.Cookie
	jar      http.CookieJar
}

func (c *reeseCookieClient) Do(_ context.Context, method, target string, body io.Reader, headers http.Header) (*http.Response, []byte, error) {
	return &http.Response{StatusCode: 200}, []byte(c.response), nil
}
func (c *reeseCookieClient) Cookies(*url.URL) []*http.Cookie { return nil }
func (c *reeseCookieClient) UserAgent() string               { return "test-browser" }
func (c *reeseCookieClient) SetCookies(u *url.URL, cookies []*http.Cookie) {
	c.set = append(c.set, cookies...)
	if c.jar != nil {
		c.jar.SetCookies(u, cookies)
	}
}

func TestReeseReplacementDoesNotSendDuplicateScopedTokens(t *testing.T) {
	for _, domain := range []string{"", "book.evaair.com", "evaair.com"} {
		jar, _ := cookiejar.New(nil)
		u, _ := url.Parse("https://book.evaair.com/")
		jar.SetCookies(u, []*http.Cookie{{Name: "reese84", Value: "legacy-host", Path: "/", Secure: true}, {Name: "reese84", Value: "legacy-parent", Domain: "evaair.com", Path: "/", Secure: true}})
		c := &reeseCookieClient{jar: jar, response: fmt.Sprintf(`{"token":"fresh-token","cookieDomain":%q,"renewInSec":900}`, domain)}
		if err := submitReese(context.Background(), c, u.String(), u.String()+"sensor", u.String(), "signed", "en-GB"); err != nil {
			t.Fatal(err)
		}
		cookies := jar.Cookies(u)
		if len(cookies) != 1 || cookies[0].Value != "fresh-token" {
			t.Fatal("conflicting old scoped token survived replacement")
		}
		sibling, _ := url.Parse("https://eservice.evaair.com/")
		if (len(jar.Cookies(sibling)) == 1) != (domain == "evaair.com") {
			t.Fatal("cookie scope differs from the validated response")
		}
	}
}

func TestReeseCookieHonorsValidatedScopeAndExpiry(t *testing.T) {
	for _, domain := range []string{"", "book.evaair.com", ".evaair.com", "other.test", "co.jp"} {
		t.Run(domain, func(t *testing.T) {
			c := &reeseCookieClient{response: fmt.Sprintf(`{"token":"fresh-token","cookieDomain":%q,"renewInSec":900}`, domain)}
			err := submitReese(context.Background(), c, "https://book.evaair.com/application", "https://book.evaair.com/sensor?d=book.evaair.com", "https://book.evaair.com", "signed-payload", "en-GB")
			if domain == "other.test" || domain == "co.jp" {
				if err == nil || len(c.set) != 0 {
					t.Fatal("untrusted domain changed the cookie jar")
				}
				return
			}
			if err != nil || len(c.set) < 2 {
				t.Fatalf("err=%v cookies=%d", err, len(c.set))
			}
			token := c.set[len(c.set)-1]
			want := domain
			if domain == ".evaair.com" {
				want = "evaair.com"
			}
			if token.Name != "reese84" || token.Domain != want || token.Value != "fresh-token" || !token.Secure || token.Path != "/" || time.Until(token.Expires) < 895*time.Second || time.Until(token.Expires) > 901*time.Second {
				t.Fatal("wrong token scope, expiry, or security attributes")
			}
			if c.set[0].MaxAge != -1 || c.set[0].Domain != "" {
				t.Fatal("legacy host-only token was not removed")
			}
		})
	}
}
