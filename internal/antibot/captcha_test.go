package antibot

import (
	"awarddesk/internal/session"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type solverTransportFunc func(*http.Request) (*http.Response, error)

func TestSolverStatusPollRecoversTemporaryFailureWithoutCreatingTask(t *testing.T) {
	calls, notices := 0, 0
	s := AnySolver{Key: "test-key", Client: &http.Client{Transport: solverTransportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/getTaskResult" {
			t.Fatal("created a duplicate task")
		}
		var request map[string]any
		if json.NewDecoder(r.Body).Decode(&request) != nil || request["taskId"] != "existing-task" {
			t.Fatal("lost existing task ID")
		}
		if calls == 1 {
			return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"errorId":0,"status":"ready"}`))}, nil
	})}}
	ctx := session.WithRetryObserver(context.Background(), func(error, int) { notices++ })
	v, err := s.call(ctx, "getTaskResult", map[string]any{"taskId": "existing-task"})
	if err != nil || v["status"] != "ready" || calls != 2 || notices != 1 {
		t.Fatalf("poll did not recover: calls=%d notices=%d err=%v", calls, notices, err)
	}
}

func TestSolverCreationAndAccessErrorsAreNotRetried(t *testing.T) {
	for _, tc := range []struct {
		path   string
		status int
		body   string
	}{
		{"createTask", 500, "unavailable"},
		{"getTaskResult", 403, "denied"},
		{"getTaskResult", 200, `{"errorId":1,"errorCode":"ERROR_ZERO_BALANCE"}`},
	} {
		calls := 0
		s := AnySolver{Key: "test-key", Client: &http.Client{Transport: solverTransportFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}}
		if _, err := s.call(context.Background(), tc.path, map[string]any{}); err == nil || calls != 1 {
			t.Fatalf("unsafe replay: path=%s calls=%d err=%v", tc.path, calls, err)
		}
	}
}

func TestCaptchaSolutionPreservesActualIdentity(t *testing.T) {
	got, err := decodeCaptchaSolution(map[string]any{"token": "test-token", "userAgent": "provider-selected-agent"})
	if err != nil || got.Token != "test-token" || got.UserAgent != "provider-selected-agent" {
		t.Fatal("solver identity metadata was lost")
	}
	got, err = decodeCaptchaSolution(map[string]any{"token": "test-token"})
	if err != nil || got.UserAgent != "" {
		t.Fatal("missing identity must remain unknown, not inferred from the request")
	}
	for _, invalid := range []any{nil, "bad", map[string]any{"userAgent": "agent"}, map[string]any{"token": 42}} {
		if _, err := decodeCaptchaSolution(invalid); err == nil {
			t.Fatal("accepted missing token")
		}
	}
}

func (f solverTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Verify the serialized external API contract without contacting a solver.
// Cancellation after task creation ensures the test cannot poll or incur cost.
func TestSolverUsesAirlineStickyProxy(t *testing.T) {
	for _, raw := range []string{"", "127.0.0.1:8123:sticky-user:secret:with:colons", "http://sticky-user:secret%40value@127.0.0.1:8123"} {
		t.Run(map[bool]string{true: "direct", false: "proxy"}[raw == ""], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			s := AnySolver{Key: "test-key", Proxy: raw, Client: &http.Client{Transport: solverTransportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "api.anysolver.com" || r.URL.Path != "/createTask" {
					t.Fatal("unexpected solver destination")
				}
				var body struct {
					Task     map[string]any `json:"task"`
					Settings map[string]any `json:"settings"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if raw == "" {
					if body.Task["type"] != "PopularCaptchaTokenProxyLess" || body.Task["proxy"] != nil {
						t.Fatal("direct session must request proxyless solving")
					}
				} else {
					if body.Settings["allowProxyReuse"] != true {
						t.Fatal("sticky proxy retry was not explicitly enabled")
					}
					if body.Task["type"] != "PopularCaptchaToken" {
						t.Fatal("proxy session requested mismatched task type")
					}
					value, ok := body.Task["proxy"].(string)
					if !ok {
						t.Fatal("missing shared proxy")
					}
					u, err := url.Parse(value)
					if err != nil {
						t.Fatal("invalid serialized proxy")
					}
					password, _ := u.User.Password()
					expected := "secret:with:colons"
					if strings.HasPrefix(raw, "http:") {
						expected = "secret@value"
					}
					if u.Host != "127.0.0.1:8123" || u.User.Username() != "sticky-user" || password != expected {
						t.Fatal("sticky proxy credentials changed")
					}
				}
				if body.Task["websiteURL"] != "https://example.test/login" || body.Task["websiteKey"] != "public-site-key" {
					t.Fatal("challenge context changed")
				}
				cancel()
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"errorId":0,"taskId":"test"}`)), Header: make(http.Header)}, nil
			})}}
			_, err := s.Solve(ctx, "https://example.test/login", "public-site-key")
			if !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatal("expected cancellation immediately after task creation")
			}
		})
	}
}

func TestSolverRejectsInvalidProxyBeforeSendingSecrets(t *testing.T) {
	called := false
	s := AnySolver{Key: "test-key", Proxy: "socks5://user:private-password@localhost:8080", Client: &http.Client{Transport: solverTransportFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected network")
	})}}
	_, err := s.Solve(context.Background(), "https://example.test/login", "site-key")
	if err == nil || called || strings.Contains(err.Error(), "private-password") {
		t.Fatal("invalid proxy was sent or leaked")
	}
}
