package antibot

import (
	"awarddesk/internal/award"
	"context"
	"errors"
	"io"
	"net/url"
	"testing"

	http "github.com/bogdanfinn/fhttp"
)

type ipClient struct {
	calls  int
	body   string
	status int
}

func (c *ipClient) Do(_ context.Context, method, target string, _ io.Reader, h http.Header) (*http.Response, []byte, error) {
	c.calls++
	if method != "GET" || target != "https://api.ipify.org?format=json" {
		return nil, nil, errors.New("unexpected request")
	}
	if len(h["x-api-key"]) != 0 {
		return nil, nil, errors.New("solver key leaked to IP verification service")
	}
	if c.status >= 400 {
		return &http.Response{StatusCode: c.status}, []byte(c.body), award.Failure("authentication", "upstream rejected request")
	}
	return &http.Response{StatusCode: 200}, []byte(c.body), nil
}

func TestIPServiceRejectionIsNotAirlineAuthenticationFailure(t *testing.T) {
	r := hyperSession{ctx: context.Background(), client: &ipClient{status: 403, body: `{"error":"access denied"}`}}
	_, err := r.outboundIP()
	var failure *award.Error
	if !errors.As(err, &failure) || failure.Code != "proxy_verification" || failure.HTTPStatus != 403 {
		t.Fatalf("wrong failure classification: %v", err)
	}
}
func (*ipClient) Cookies(*url.URL) []*http.Cookie { return nil }
func (*ipClient) UserAgent() string               { return "" }

func TestOutboundIPCachedWithinSession(t *testing.T) {
	c := &ipClient{body: `{"ip":"192.0.2.1"}`}
	r := hyperSession{ctx: context.Background(), client: c}
	for i := 0; i < 3; i++ {
		ip, err := r.outboundIP()
		if err != nil || ip != "192.0.2.1" {
			t.Fatalf("IP lookup failed: %v", err)
		}
	}
	if c.calls != 1 {
		t.Fatalf("made %d IP requests", c.calls)
	}
	next := hyperSession{ctx: context.Background(), client: c}
	if _, err := next.outboundIP(); err != nil {
		t.Fatal(err)
	}
	if c.calls != 2 {
		t.Fatal("IP cache leaked between sessions")
	}
}
func TestInvalidIPIsNotCached(t *testing.T) {
	c := &ipClient{body: `{"ip":"invalid"}`}
	r := hyperSession{ctx: context.Background(), client: c}
	if _, err := r.outboundIP(); err == nil {
		t.Fatal("invalid IP accepted")
	}
	c.body = `{"ip":"192.0.2.2"}`
	if _, err := r.outboundIP(); err != nil {
		t.Fatal(err)
	}
	if c.calls != 2 {
		t.Fatal("failed lookup was cached")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.ctx = ctx
	if _, err := r.outboundIP(); !errors.Is(err, context.Canceled) {
		t.Fatal("cached lookup ignored cancellation")
	}
}
