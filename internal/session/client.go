package session

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/cookiejar"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"

	"awarddesk/internal/award"
)

const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36"
const Language = "en-GB,en;q=0.9"

type Client struct {
	http             tlsclient.HttpClient
	hosts            map[string]bool
	count            int
	started          bool
	max              int
	userAgent        string
	chromeVersion    string
	egressIP         string
	jar              *recordingJar
	transportTimeout time.Duration
}

var browserUserAgent = regexp.MustCompile(`^Mozilla/5\.0 \(Windows NT 10\.0; Win64; x64\) AppleWebKit/537\.36 \(KHTML, like Gecko\) Chrome/([0-9]{2,3})\.0\.0\.0 Safari/537\.36$`)

func New(proxy string, hosts []string) (*Client, error) {
	return NewWithTimeout(proxy, hosts, 40*time.Second)
}

func NewWithTimeout(proxy string, hosts []string, timeout time.Duration) (*Client, error) {
	if timeout < time.Second || timeout > 2*time.Minute {
		return nil, errors.New("invalid HTTP transport timeout")
	}
	p, e := ParseProxy(proxy)
	if e != nil {
		return nil, e
	}
	jar, e := cookiejar.New(nil)
	if e != nil {
		return nil, e
	}
	recorded := &recordingJar{CookieJar: jar}
	opts := []tlsclient.HttpClientOption{tlsclient.WithClientProfile(profiles.Chrome_152), tlsclient.WithRandomTLSExtensionOrder(), tlsclient.WithDisableHttp3(), tlsclient.WithNotFollowRedirects(), tlsclient.WithTimeoutMilliseconds(int(timeout.Milliseconds())), tlsclient.WithCookieJar(recorded)}
	if p != "" {
		opts = append(opts, tlsclient.WithProxyUrl(p))
	}
	c, e := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), opts...)
	if e != nil {
		return nil, errors.New("could not initialize HTTP session")
	}
	allowed := map[string]bool{}
	for _, h := range hosts {
		allowed[h] = true
	}
	return &Client{http: c, jar: recorded, hosts: allowed, max: 180, userAgent: UserAgent, chromeVersion: "155", transportTimeout: timeout}, nil
}
func (c *Client) UserAgent() string { return c.userAgent }

func (c *Client) BeginOperation() { c.count = 0 }

func (c *Client) PinEgressIP(value string) error {
	ip := net.ParseIP(value)
	if ip == nil {
		return award.Failure("proxy_ip", "Proxy IP verification returned an invalid address")
	}
	if c.egressIP != "" && !net.ParseIP(c.egressIP).Equal(ip) {
		return award.Failure("proxy_changed", "Proxy exit IP changed during login; credentials were not submitted")
	}
	c.egressIP = ip.String()
	return nil
}

func (c *Client) SetUserAgent(value string) error {
	if c.started || c.count != 0 {
		return errors.New("cannot change user-agent after session initialization")
	}
	match := browserUserAgent.FindStringSubmatch(value)
	if match == nil {
		return errors.New("unsupported HTTP user-agent")
	}
	c.userAgent, c.chromeVersion = value, match[1]
	return nil
}
func (c *Client) Close()                                  { c.http.CloseIdleConnections() }
func (c *Client) Cookies(u *url.URL) []*http.Cookie       { return c.http.GetCookies(u) }
func (c *Client) SetCookies(u *url.URL, v []*http.Cookie) { c.http.SetCookies(u, v) }
func Pause(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func Headers(extra http.Header) http.Header {
	h := http.Header{"sec-ch-ua": {`"Google Chrome";v="155", "Chromium";v="155", "Not(A:Brand";v="24"`}, "sec-ch-ua-mobile": {"?0"}, "sec-ch-ua-platform": {`"Windows"`}, "upgrade-insecure-requests": {"1"}, "user-agent": {UserAgent}, "accept": {"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8"}, "sec-fetch-site": {"none"}, "sec-fetch-mode": {"navigate"}, "sec-fetch-user": {"?1"}, "sec-fetch-dest": {"document"}, "accept-encoding": {"gzip, deflate, br, zstd"}, "accept-language": {Language}, "priority": {"u=0, i"}}
	for k, v := range extra {
		h[strings.ToLower(k)] = v
	}
	if dest := h["sec-fetch-dest"]; len(dest) > 0 && dest[0] == "iframe" {
		delete(h, "sec-fetch-user")
	}
	if m := h["sec-fetch-mode"]; len(m) > 0 && (m[0] == "cors" || m[0] == "no-cors") {
		delete(h, "upgrade-insecure-requests")
		delete(h, "sec-fetch-user")
		h["priority"] = []string{"u=1, i"}
		if m[0] == "no-cors" {
			h["accept"] = []string{"*/*"}
		}
	}
	h[http.PHeaderOrderKey] = []string{":method", ":authority", ":scheme", ":path"}
	h[http.HeaderOrderKey] = []string{"content-length", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "upgrade-insecure-requests", "user-agent", "accept", "authorization", "origin", "content-type", "x-xsrf-token", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-user", "sec-fetch-dest", "referer", "accept-encoding", "accept-language", "cookie", "priority"}
	if mode := h["sec-fetch-mode"]; len(mode) > 0 && mode[0] == "navigate" {

		h[http.HeaderOrderKey] = []string{"content-length", "cache-control", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "upgrade-insecure-requests", "content-type", "user-agent", "origin", "accept", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-user", "sec-fetch-dest", "referer", "accept-encoding", "accept-language", "cookie", "priority"}
	}
	return h
}
func (c *Client) Do(ctx context.Context, method, raw string, body io.Reader, extra http.Header) (*http.Response, []byte, error) {
	timeout := 40 * time.Second
	if c.transportTimeout > 0 && c.transportTimeout < timeout {
		timeout = c.transportTimeout
	}
	return c.DoWithTimeout(ctx, method, raw, body, extra, timeout)
}

func (c *Client) DoWithTimeout(ctx context.Context, method, raw string, body io.Reader, extra http.Header, timeout time.Duration) (*http.Response, []byte, error) {
	if timeout <= 0 || timeout > 2*time.Minute || (c.transportTimeout > 0 && timeout > c.transportTimeout) {
		return nil, nil, errors.New("invalid provider request timeout")
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || !c.hosts[u.Host] {
		return nil, nil, award.Failure("unsafe_destination", "unexpected destination in provider flow")
	}

	tries := 1
	if method == "GET" {
		tries = 2
	}
	for attempt := 0; attempt < tries; attempt++ {
		if c.count >= c.max {
			return nil, nil, award.Failure("request_limit", "search request limit reached")
		}
		if e = ctx.Err(); e != nil {
			return nil, nil, e
		}
		c.count++
		c.started = true
		requestCtx, cancelRequest := context.WithTimeout(ctx, timeout)
		defer cancelRequest()
		req, e := http.NewRequestWithContext(requestCtx, method, raw, body)
		if e != nil {
			return nil, nil, errors.New("invalid request")
		}
		req.Header = Headers(extra)
		req.Header["user-agent"] = []string{c.userAgent}
		req.Header["sec-ch-ua"] = []string{`"Google Chrome";v="` + c.chromeVersion + `", "Chromium";v="` + c.chromeVersion + `", "Not(A:Brand";v="24"`}
		if mode := req.Header["sec-fetch-mode"]; method == "POST" && len(mode) > 0 && mode[0] == "navigate" {
			req.Header["cache-control"] = []string{"max-age=0"}
		}
		res, e := c.http.Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			if attempt+1 < tries {
				ReportRetry(ctx, transportFailure(e), attempt+1)
				if e = Pause(ctx, 2*time.Second); e != nil {
					return nil, nil, e
				}
				continue
			}
			return nil, nil, transportFailure(e)
		}
		b, e := io.ReadAll(io.LimitReader(res.Body, 8*1024*1024+1))
		res.Body.Close()
		if e != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			if requestCtx.Err() != nil {
				e = transportFailure(requestCtx.Err())
			} else {
				e = award.Failure("response_read", "could not read provider response")
			}
			if attempt+1 < tries {
				ReportRetry(ctx, e, attempt+1)
				if pauseErr := Pause(ctx, 2*time.Second); pauseErr != nil {
					return nil, nil, pauseErr
				}
				continue
			}
			return nil, nil, e
		}
		if len(b) > 8*1024*1024 {
			return nil, nil, award.Failure("response_size", "provider response exceeded size limit")
		}
		if res.StatusCode == 429 || res.Header.Get("Retry-After") != "" {
			delay := 60
			if n, er := strconv.Atoi(res.Header.Get("Retry-After")); er == nil && n > 0 {
				delay = n
			} else if t, er := http.ParseTime(res.Header.Get("Retry-After")); er == nil && time.Until(t) > 0 {
				delay = int(time.Until(t).Seconds()) + 1
			}
			return nil, nil, &award.Error{Code: "rate_limited", Message: "provider requested a pause", RetryAfterSeconds: delay}
		}
		if res.StatusCode >= 500 && attempt+1 < tries {
			ReportRetry(ctx, award.Failure("site_unavailable", "provider returned a temporary server error"), attempt+1)
			if e = Pause(ctx, 2*time.Second); e != nil {
				return nil, nil, e
			}
			continue
		}
		if res.StatusCode == 401 || res.StatusCode == 403 {
			return res, b, &award.Error{Code: "authentication", Message: "provider rejected the session (HTTP " + strconv.Itoa(res.StatusCode) + ")", HTTPStatus: res.StatusCode}
		}
		if res.StatusCode >= 400 {

			return res, b, &award.Error{Code: "provider_http", Message: "provider returned HTTP " + strconv.Itoa(res.StatusCode), HTTPStatus: res.StatusCode}
		}
		return res, b, nil
	}
	return nil, nil, errors.New("request attempts exhausted")
}

func transportFailure(err error) error {
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return award.Failure("connection", "provider request timed out; request was not replayed")
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "407") || strings.Contains(text, "proxy authentication") {
		return award.Failure("proxy_authentication", "proxy authentication failed; check the proxy list")
	}
	if strings.Contains(text, "reset") || strings.Contains(text, "eof") || strings.Contains(text, "closed") {
		return award.Failure("connection", "provider connection closed before completion; request was not replayed")
	}
	return award.Failure("connection", "provider connection could not complete; request was not replayed")
}
