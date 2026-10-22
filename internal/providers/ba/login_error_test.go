package ba

import (
	"errors"
	"strings"
	"testing"

	"awarddesk/internal/award"
)

func TestLoginRejectionsAreSpecificAndRedacted(t *testing.T) {
	for _, tc := range []struct{ body, code string }{
		{`<div role="alert">Captcha validation failed</div>`, "login_captcha_rejected"},
		{`<div role="alert">Wrong email or password.</div>`, "login_credentials_rejected"},
		{`<div role="alert">Your session has expired.</div>`, "login_session_rejected"},
		{`<div role="alert">Too many attempts.</div>`, "login_rate_limited"},
		{`<h1>Bad request</h1>`, "login_rejected"},
		{`<script>const messages={captcha_invalid:"Invalid captcha"};</script><h1>Bad request</h1>`, "login_rejected"},
		{`<span class="ulp-input-error-message hide">Invalid captcha</span>`, "login_rejected"},
		{`<div hidden>Incorrect password</div>`, "login_rejected"},
		{`<div aria-hidden="true">Session expired</div>`, "login_rejected"},
		{`<span class="hide">Invalid captcha</span><div role="alert">Wrong email or password.</div>`, "login_credentials_rejected"},
	} {
		var e *award.Error
		err := classifyLoginRejection([]byte(tc.body+`<input value="PRIVATE-SECRET">`), 400)
		if !errors.As(err, &e) || e.Code != tc.code || e.HTTPStatus != 400 {
			t.Fatalf("unexpected classification: %v", err)
		}
		if strings.Contains(e.Message, "PRIVATE-SECRET") {
			t.Fatal("rejection leaked response data")
		}
	}
}
