package ana

import (
	"awarddesk/internal/award"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"
)

type loginStub struct {
	t        *testing.T
	calls    int
	sso      string
	member   string
	auth     string
	failAt   int
	captured []http.Header
}

func (s *loginStub) Do(ctx context.Context, method, target string, body io.Reader, h http.Header) (*http.Response, []byte, error) {
	s.t.Helper()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	i := s.calls
	s.calls++
	s.captured = append(s.captured, h)
	methods := []string{"GET", "GET", "POST", "POST", "GET"}
	targets := []string{home, "https://www.ana.co.jp/api-secret/client.json", api + "/auth/api/v2/authorization", api + "/auth/api/v2/sso-authentication", api + "/amcmember-decrypt/api/v1/cookie?ctryCod=eu"}
	if i >= len(targets) {
		s.t.Fatal("unexpected extra request (login must not search or retry credentials)")
	}
	if method != methods[i] || target != targets[i] {
		s.t.Fatalf("unexpected login step %d: %s %s", i, method, target)
	}
	if i >= 2 {
		if h["client_id"][0] != "synthetic-client" || h["client_secret"][0] != "synthetic-key" || h["sys_id"][0] != "ASW" {
			s.t.Fatal("missing dynamic frontend API headers")
		}
	}
	if i == 2 || i == 3 {
		var payload map[string]string
		if err := json.NewDecoder(body).Decode(&payload); err != nil {
			s.t.Fatal(err)
		}
		if i == 2 && (payload["usableState"] != "3" || payload["personalUpdateFlag"] != "") {
			s.t.Fatal("incorrect authorization payload")
		}
		if i == 3 && (payload["mebrNo"] != "0000000000" || payload["mebrPswd"] != "synthetic-password" || payload["persistentFlag"] != "false" || payload["auth"] != "web" || len(payload) != 7) {
			s.t.Fatal("incorrect SSO payload")
		}
	}
	if i == 3 && (h["authorization"][0] != "Bearer fresh-token" || h["x-correlation-id"][0] != "fresh-correlation") {
		s.t.Fatal("authorization state was not propagated")
	}
	if s.failAt == i+1 {
		return nil, nil, award.Failure("authentication", "synthetic rejection")
	}
	responses := []string{`<html></html>`, `{"client_id":"synthetic-client","client_secret":"synthetic-key"}`, s.auth, s.sso, s.member}
	headers := http.Header{}
	if i == 2 {
		headers.Set("x-correlation-id", "fresh-correlation")
	}
	return &http.Response{StatusCode: 200, Header: headers}, []byte(responses[i]), nil
}

func stub(t *testing.T) *loginStub {
	return &loginStub{t: t, auth: `{"status":"1","loginState":"3","access_token":"fresh-token"}`, sso: `{"param":"","access_token":"","twoStepAuth":"false","score":""}`, member: `{"personal":{"mainMebrNo":"0000000000"}}`}
}

func flow(s *loginStub) *loginFlow {
	return &loginFlow{client: s, config: Config{MemberNumber: "0000000000", Password: "synthetic-password"}}
}

func TestLoginFreshStateAndTokenRetention(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		s := stub(t)
		want := "fresh-token"
		if replacement {
			s.sso = `{"param":"","access_token":"replacement","twoStepAuth":"false"}`
			want = "replacement"
		}
		f := flow(s)
		if err := f.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if s.calls != 5 || f.token != want || s.captured[4]["authorization"][0] != "Bearer "+want {
			t.Fatal("login did not verify membership with the current token")
		}
	}
}

func TestLoginFailureStates(t *testing.T) {
	tests := []struct {
		name, sso, member, auth, code string
		calls                         int
	}{
		{name: "MFA", sso: `{"twoStepAuth":"true"}`, code: "mfa_required", calls: 4},
		{name: "browser", sso: `{"param":"sensitive-value","twoStepAuth":"false"}`, code: "browser_required", calls: 4},
		{name: "missing state", sso: `{}`, code: "login_response", calls: 4},
		{name: "changed schema", sso: `{"twoStepAuth":false}`, code: "login_response", calls: 4},
		{name: "upstream error", sso: `{"errorCode":["sensitive-value"]}`, code: "login_response", calls: 4},
		{name: "HTML challenge", sso: `<html>sensitive-value</html>`, code: "login_response", calls: 4},
		{name: "wrong member", member: `{"personal":{"mainMebrNo":"1111111111"}}`, code: "login_unverified", calls: 5},
		{name: "anonymous", member: `{}`, code: "login_unverified", calls: 5},
		{name: "authorization failure", auth: `{"status":"0","loginState":"3","access_token":"sensitive-value"}`, code: "login_authorization", calls: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stub(t)
			if tt.sso != "" {
				s.sso = tt.sso
			}
			if tt.member != "" {
				s.member = tt.member
			}
			if tt.auth != "" {
				s.auth = tt.auth
			}
			err := flow(s).run(context.Background())
			var ae *award.Error
			if !errors.As(err, &ae) || ae.Code != tt.code || s.calls != tt.calls {
				t.Fatalf("unexpected failure: %v; calls=%d", err, s.calls)
			}
			if strings.Contains(err.Error(), "sensitive-value") {
				t.Fatal("upstream data leaked")
			}
		})
	}
}

func TestLoginDoesNotRetryCredentials(t *testing.T) {
	s := stub(t)
	s.failAt = 4
	if err := flow(s).run(context.Background()); err == nil || s.calls != 4 {
		t.Fatal("credential rejection must stop immediately")
	}
}

func TestLoginCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := stub(t)
	if err := flow(s).run(ctx); !errors.Is(err, context.Canceled) || s.calls != 0 {
		t.Fatal("cancelled login must not make requests")
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, c := range []Config{{}, {MemberNumber: "123", Password: "password"}, {MemberNumber: "abcdefghij", Password: "password"}, {MemberNumber: "0000000000", Password: "1234"}} {
		if _, err := New(c).Login(context.Background(), nil); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}
