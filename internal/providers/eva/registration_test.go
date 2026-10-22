package eva

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"awarddesk/internal/award"
)

func TestRegistrationProfilesMeetFormConstraints(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		p, e := NewRegistrationProfile()
		if e != nil {
			t.Fatal(e)
		}
		if len(p.Password) < 8 || len(p.Password) > 30 || !strings.Contains(p.Password, "!") || !regexp.MustCompile(`[A-Z]`).MatchString(p.Password) || !regexp.MustCompile(`[a-z]`).MatchString(p.Password) || !regexp.MustCompile(`[0-9]`).MatchString(p.Password) {
			t.Fatal("invalid generated password")
		}
		if seen[p.Password] {
			t.Fatal("password reused")
		}
		seen[p.Password] = true
		if !regexp.MustCompile(`^[A-Za-z]{2,20}$`).MatchString(p.FirstName) || !regexp.MustCompile(`^[A-Za-z]{2,20}$`).MatchString(p.LastName) || !regexp.MustCompile(`^[0-9]{10}$`).MatchString(p.Phone) {
			t.Fatal("invalid profile")
		}
		if _, e = time.Parse("2006-1-2", p.Year+"-"+p.Month+"-"+p.Day); e != nil {
			t.Fatal(e)
		}
	}
}

func TestRegistrationAcknowledgementRequiresProviderCompletionContainer(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`<main id="content_pal_Done"><h1>Complete</h1></main>`, true},
		{`<main id='content_pal_Done'>Membership email has been sent</main>`, true},
		{`<main id="content_pal_Terms">Complete your registration</main>`, false},
		{`<script>const markup='<main id="content_pal_Done">'</script>`, false},
		{`<h1>Access denied</h1>`, false},
	} {
		if registrationAcknowledged([]byte(tc.body)) != tc.want {
			t.Fatal("incorrect confirmation classification")
		}
	}
}

func TestRejectedRegistrationDoesNotBecomeEmailTimeout(t *testing.T) {
	rejected := &award.Error{Code: "session_denied", HTTPStatus: 403, Message: "EVA rejected registration"}
	if registrationSubmissionFailure(nil, "fixture@example.test", "private", rejected) != rejected {
		t.Fatal("original rejection was discarded")
	}
	if registrationSubmissionFailure([]byte(`<form>Unrecognised response</form>`), "fixture@example.test", "private", nil) == nil {
		t.Fatal("unconfirmed response accepted")
	}
	if registrationSubmissionFailure([]byte(`<main id="content_pal_Done"></main>`), "fixture@example.test", "private", nil) != nil {
		t.Fatal("confirmed submission rejected")
	}
}
func TestLoginStageMarksOnlyUncertainSubmittedMutations(t *testing.T) {
	for _, stage := range []string{"credentials", "otp submission", "verification continuation", "proxy verification", "adaptive security"} {
		original := &award.Error{Code: "connection", Message: "interrupted"}
		err := loginStageError(original, stage)
		var failure *award.Error
		if !errors.As(err, &failure) || failure.Stage != stage {
			t.Fatal("missing stage")
		}
		want := stage == "credentials" || stage == "otp submission" || stage == "verification continuation"
		if failure.OutcomeUnknown != want || original.OutcomeUnknown || original.Stage != "" {
			t.Fatal("incorrect mutation classification or shared error modified")
		}
	}
}
func TestPortalContinuationCannotCrossEndpointsOrLoseWrapper(t *testing.T) {
	p := New(Config{})
	for _, action := range []string{"https://evil.example/register?akamai-challenge-resubmit=true", LoginURL + "?akamai-challenge-resubmit=true", RegistrationURL + "?other=true"} {
		body := []byte(`<form action="` + action + `"><input name="sec_chlge_forward_wrap" value="opaque"><input name="sec_chlge_content_type_wrap" value="opaque-type"></form>`)
		if _, _, e := p.resumePortalChallenge(context.Background(), nil, body, RegistrationURL, "/flyeva/EVA/FFP/register-now.aspx", func(string, string) {}); e == nil {
			t.Fatal("unsafe continuation accepted")
		}
	}
	body := []byte(`<form action="` + RegistrationURL + `?akamai-challenge-resubmit=true"><input name="sec_chlge_forward_wrap" value="opaque"></form>`)
	if _, _, e := p.resumePortalChallenge(context.Background(), nil, body, RegistrationURL, "/flyeva/EVA/FFP/register-now.aspx", func(string, string) {}); e == nil {
		t.Fatal("missing wrapper reached solver")
	}
}
