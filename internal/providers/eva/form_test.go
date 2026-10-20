package eva

import "testing"

func TestFreshFormStateAndCaptcha(t *testing.T) {
	body := []byte(`<html><head><title>EVA Award Login</title></head><body><form action="login.aspx?lang=en-global"><input name="__ZIPSTATE" value="fresh&amp;state"><input name="ctl00$content$wuc_login$txt_Member"><input name="ctl00$content$wuc_login$txt_Password" type="password"><input name="ctl00$content$wuc_login$grecaptcharesponse"><input name="ignored" type="checkbox"><input name="checked" type="checkbox" checked value="on"><input name="submit" type="submit"></form><script>grecaptcha.execute("site-key", { action: "wucLogin" })</script></body></html>`)
	f, err := parseForm(body, LoginURL)
	if err != nil {
		t.Fatal(err)
	}
	if f.key != "site-key" || f.captchaAction != "wucLogin" || f.values.Get("__ZIPSTATE") != "fresh&state" || f.values.Has("ignored") || f.values.Has("submit") || f.values.Get("checked") != "on" {
		t.Fatal("form state changed")
	}
	if err = f.set("$txt_Member", "test@example.test"); err != nil {
		t.Fatal(err)
	}
	if authenticated(body, ItineraryURL) {
		t.Fatal("login form treated as signed in")
	}
	if authenticated([]byte(`<form><input name="reese84-resubmit-data"><p>select-itinerary.aspx</p></form>`), ItineraryURL) {
		t.Fatal("challenge treated as signed in")
	}
}
