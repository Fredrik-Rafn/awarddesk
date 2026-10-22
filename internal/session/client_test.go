package session

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	std "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	http "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/cookiejar"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"

	"awarddesk/internal/award"
)

func TestPinnedProxyIPAndSafeTransportErrors(t *testing.T) {
	c := &Client{}
	if err := c.PinEgressIP("192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	if err := c.PinEgressIP("::ffff:192.0.2.1"); err != nil {
		t.Fatal("equivalent address rejected")
	}
	var ae *award.Error
	if err := c.PinEgressIP("192.0.2.2"); !errors.As(err, &ae) || ae.Code != "proxy_changed" {
		t.Fatal("proxy change was not detected")
	}
	if c.egressIP != "192.0.2.1" {
		t.Fatal("failed verification replaced pinned IP")
	}
	for _, raw := range []string{
		"proxy authentication failed http://private:secret@proxy.test:1000",
		"connection reset http://private:secret@proxy.test:1000",
		"unexpected EOF http://private:secret@proxy.test:1000",
	} {
		err := transportFailure(errors.New(raw))
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private") {
			t.Fatal("transport diagnostic leaked credentials")
		}
	}
}

func TestProxyFormats(t *testing.T) {
	exported, err := ParseProxy("http://proxy.example:1000:user:pass_session-EXAMPLE12")
	if err != nil || exported != "http://user:pass_session-EXAMPLE12@proxy.example:1000" {
		t.Fatal("scheme-prefixed proxy export was not normalized")
	}
	parsed, err := ParseProxy("127.0.0.1:8000:user:https://password")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(parsed)
	if pass, _ := u.User.Password(); pass != "https://password" {
		t.Fatal("URL-like proxy password changed")
	}
	for _, raw := range []string{"127.0.0.1:8000:sticky-session-123:p:a@ss", "http://sticky-session-123:p%3Aa%40ss@127.0.0.1:8000"} {
		p, e := ParseProxy(raw)
		if e != nil {
			t.Fatal(e)
		}
		u, _ := url.Parse(p)
		pass, _ := u.User.Password()
		if u.User.Username() != "sticky-session-123" || pass != "p:a@ss" || u.Host != "127.0.0.1:8000" {
			t.Fatal("proxy credentials changed")
		}
	}
	for _, raw := range []string{"host:0:user:secret", "socks5://user:secret@host:80", "host:80:user:", "http://user:secret@host:80/path", "http://host", "http://host:abc"} {
		_, e := ParseProxy(raw)
		if e == nil {
			t.Fatalf("accepted malformed proxy %q", raw)
		}
		if strings.Contains(e.Error(), "secret") {
			t.Fatal("error leaked credential")
		}
	}
}

func TestIdentityCannotChangeWithinSession(t *testing.T) {
	c, err := New("", []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	agent := strings.Replace(UserAgent, "155.0.0.0", "143.0.0.0", 1)
	if err := c.SetUserAgent(agent); err != nil {
		t.Fatal(err)
	}
	if c.UserAgent() != agent || c.chromeVersion != "143" {
		t.Fatal("identity components disagree")
	}
	if err := c.SetUserAgent("invalid\r\ninjected: header"); err == nil {
		t.Fatal("accepted invalid identity")
	}
	c.count = 1
	if err := c.SetUserAgent(UserAgent); err == nil || c.UserAgent() != agent {
		t.Fatal("changed an initialized session")
	}
}

func TestInFlightCancellation(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewTLSServer(std.HandlerFunc(func(w std.ResponseWriter, r *std.Request) { close(entered); <-r.Context().Done() }))
	defer server.Close()
	c := testClient(t, server.URL, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, e := c.Do(ctx, "GET", server.URL, nil, nil); done <- e }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatalf("cancellation became %v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight request ignored cancellation")
	}
}

func TestExplicitPricingDeadlineDoesNotReplayPOST(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(std.HandlerFunc(func(w std.ResponseWriter, r *std.Request) {
		calls.Add(1)
		select {
		case <-r.Context().Done():
			return
		case <-time.After(150 * time.Millisecond):
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	c := testClient(t, server.URL, "")
	_, _, err := c.DoWithTimeout(context.Background(), "POST", server.URL, strings.NewReader("price"), nil, 70*time.Millisecond)
	var ae *award.Error
	if !errors.As(err, &ae) || ae.Code != "connection" || calls.Load() != 1 {
		t.Fatalf("POST timeout was replayed or misclassified: %v", err)
	}
	_, body, err := c.DoWithTimeout(context.Background(), "POST", server.URL, strings.NewReader("price"), nil, time.Second)
	if err != nil || string(body) != "ok" || calls.Load() != 2 {
		t.Fatal("explicit longer pricing deadline did not complete")
	}
}

func TestTruncatedGETResponseRetriesOnce(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(std.HandlerFunc(func(w std.ResponseWriter, r *std.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("short"))
			return
		}
		_, _ = w.Write([]byte("complete"))
	}))
	defer server.Close()
	c := testClient(t, server.URL, "")
	notices := 0
	ctx := WithRetryObserver(context.Background(), func(err error, attempt int) {
		var failure *award.Error
		if !errors.As(err, &failure) || failure.Code != "response_read" || attempt != 1 {
			t.Fatalf("bad recovery details: %v", err)
		}
		notices++
	})
	_, body, err := c.Do(ctx, "GET", server.URL, nil, nil)
	if err != nil || string(body) != "complete" || calls.Load() != 2 {
		t.Fatalf("truncated GET not recovered: calls=%d err=%v", calls.Load(), err)
	}
	if notices != 1 {
		t.Fatalf("retry history missing: %d", notices)
	}
}

func TestTruncatedPOSTResponseIsNotReplayed(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(std.HandlerFunc(func(w std.ResponseWriter, r *std.Request) {
		calls.Add(1)
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	defer server.Close()
	c := testClient(t, server.URL, "")
	_, _, err := c.Do(context.Background(), "POST", server.URL, strings.NewReader("operation"), nil)
	if err == nil || calls.Load() != 1 {
		t.Fatalf("POST replayed: calls=%d err=%v", calls.Load(), err)
	}
}
func testClient(t *testing.T, serverURL, proxy string) *Client {
	t.Helper()
	u, _ := url.Parse(serverURL)
	jar, _ := cookiejar.New(nil)
	opts := []tlsclient.HttpClientOption{tlsclient.WithClientProfile(profiles.Chrome_152), tlsclient.WithInsecureSkipVerify(), tlsclient.WithTimeoutSeconds(3), tlsclient.WithNotFollowRedirects(), tlsclient.WithCookieJar(jar)}
	if proxy != "" {
		opts = append(opts, tlsclient.WithProxyUrl(proxy))
	}
	h, e := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), opts...)
	if e != nil {
		t.Fatal(e)
	}
	c := &Client{http: h, hosts: map[string]bool{u.Host: true}, max: 20}
	t.Cleanup(c.Close)
	return c
}
func TestAuthenticatedProxyAndCookieIsolation(t *testing.T) {
	target := httptest.NewTLSServer(std.HandlerFunc(func(w std.ResponseWriter, r *std.Request) {
		if r.URL.Path == "/set" {
			std.SetCookie(w, &std.Cookie{Name: "session", Value: "test-cookie", Path: "/"})
		}
		io.WriteString(w, r.Header.Get("Cookie"))
	}))
	defer target.Close()
	var connects atomic.Int32
	proxy := httptest.NewServer(std.HandlerFunc(func(w std.ResponseWriter, r *std.Request) {
		if r.Method != "CONNECT" || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("sticky:pass:word")) {
			w.WriteHeader(407)
			return
		}
		connects.Add(1)
		up, e := net.Dial("tcp", r.Host)
		if e != nil {
			w.WriteHeader(502)
			return
		}
		down, buffer, e := w.(std.Hijacker).Hijack()
		if e != nil {
			up.Close()
			return
		}
		defer down.Close()
		defer up.Close()
		buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		buffer.Flush()
		go io.Copy(up, down)
		io.Copy(down, up)
	}))
	defer proxy.Close()
	u, _ := url.Parse(proxy.URL)
	p, e := ParseProxy(u.Host + ":sticky:pass:word")
	if e != nil {
		t.Fatal(e)
	}
	c := testClient(t, target.URL, p)
	ctx := context.Background()
	if _, _, e = c.Do(ctx, "GET", target.URL+"/set", nil, nil); e != nil {
		t.Fatal(e)
	}
	_, b, e := c.Do(ctx, "GET", target.URL, nil, nil)
	if e != nil || !strings.Contains(string(b), "session=test-cookie") {
		t.Fatalf("same session cookie lost: %v", e)
	}
	other := testClient(t, target.URL, p)
	_, b, e = other.Do(ctx, "GET", target.URL, nil, nil)
	if e != nil || len(b) != 0 {
		t.Fatalf("new session inherited cookie: %v", e)
	}
	if connects.Load() < 2 {
		t.Fatal("sessions did not use authenticated proxy")
	}
}
func TestRetriesRateLimitAndCancellation(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewTLSServer(std.HandlerFunc(func(w std.ResponseWriter, r *std.Request) {
		calls.Add(1)
		if r.URL.Path == "/rate" {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(503)
	}))
	defer target.Close()
	c := testClient(t, target.URL, "")
	ctx := context.Background()
	c.Do(ctx, "POST", target.URL, nil, nil)
	if calls.Load() != 1 {
		t.Fatal("POST was retried")
	}
	c.Do(ctx, "GET", target.URL, nil, nil)
	if calls.Load() != 3 {
		t.Fatal("GET did not retry once")
	}
	_, _, e := c.Do(ctx, "GET", target.URL+"/rate", nil, nil)
	var ae *award.Error
	if !errors.As(e, &ae) || ae.RetryAfterSeconds != 120 || calls.Load() != 4 {
		t.Fatalf("rate limit: %v", e)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, e = c.Do(cancelled, "GET", target.URL, nil, nil)
	if !errors.Is(e, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
	if calls.Load() != 4 {
		t.Fatal("cancelled request reached the server")
	}
	_, _, e = c.Do(ctx, "GET", "https://untrusted.example/", nil, nil)
	if !errors.As(e, &ae) || ae.Code != "unsafe_destination" {
		t.Fatal("host allowlist bypass")
	}
}
func TestXHRHeaders(t *testing.T) {
	h := Headers(http.Header{"Sec-Fetch-Mode": {"cors"}, "Content-Type": {"application/json"}})
	if _, ok := h["upgrade-insecure-requests"]; ok {
		t.Fatal("XHR navigation header")
	}
	if len(h[http.HeaderOrderKey]) == 0 || h["content-type"][0] != "application/json" {
		t.Fatal("missing ordered headers")
	}
}

func TestIframeNavigationHasNoUserActivation(t *testing.T) {
	h := Headers(http.Header{"sec-fetch-mode": {"navigate"}, "sec-fetch-dest": {"iframe"}, "origin": {"null"}})
	if _, ok := h["sec-fetch-user"]; ok {
		t.Fatal("script-created iframe advertised user activation")
	}
	if h["upgrade-insecure-requests"][0] != "1" || h["origin"][0] != "null" {
		t.Fatal("iframe navigation context changed")
	}
	top := Headers(nil)
	if top["sec-fetch-user"][0] != "?1" {
		t.Fatal("top-level navigation identity changed")
	}
}

func TestWarmOperationResetsBudgetWithoutResettingIdentity(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewTLSServer(std.HandlerFunc(func(w std.ResponseWriter, r *std.Request) { calls.Add(1); w.Write([]byte("ok")) }))
	defer target.Close()
	c := testClient(t, target.URL, "")
	c.max = 1
	if _, _, err := c.Do(context.Background(), "GET", target.URL, nil, nil); err != nil {
		t.Fatal(err)
	}
	saved := State{UserAgent: UserAgent}
	c.BeginOperation()
	if err := c.SetUserAgent(UserAgent); err == nil {
		t.Fatal("budget reset unlocked user agent")
	}
	if err := c.Restore(saved); err == nil {
		t.Fatal("budget reset unlocked session restoration")
	}
	if _, _, err := c.Do(context.Background(), "GET", target.URL, nil, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("operation budget not reset")
	}
}
