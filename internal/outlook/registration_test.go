package outlook

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRegistrationMailIsFreshScopedAndUnambiguous(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	raw := fmt.Sprintf("From: %s\r\nTo: test@outlook.com\r\nSubject: %s\r\nDate: %s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n<p>Verification Code:ab7x29</p><a href=3D\"https://eservice.evaair.com/flyeva/EVA/FFP/register-now.aspx?test=3Dtoken\">Verify</a>", sender, registrationSubject, now.Format(time.RFC1123Z))
	msg, err := ExtractRegistration([]byte(raw), "test@outlook.com", now.Add(-time.Second), now)
	if err != nil || msg.Code != "ab7x29" || !strings.HasSuffix(msg.Link, "?test=token") {
		t.Fatal("valid registration email rejected", err)
	}
	for _, bad := range []string{
		strings.Replace(raw, sender, "fake@example.com", 1),
		strings.Replace(raw, "test@outlook.com", "other@outlook.com", 1),
		strings.Replace(raw, registrationSubject, subject, 1),
		strings.Replace(raw, "eservice.evaair.com", "evil.example", 1),
		raw + "<p>Verification Code:xx1234</p>",
		raw + " https://eservice.evaair.com/flyeva/EVA/FFP/register-now.aspx?other=3Dtoken",
	} {
		if _, e := ExtractRegistration([]byte(bad), "test@outlook.com", now.Add(-time.Second), now); e == nil {
			t.Fatal("unsafe registration email accepted")
		}
	}
	if _, e := ExtractRegistration([]byte(raw), "test@outlook.com", now.Add(time.Minute), now.Add(time.Minute)); e == nil {
		t.Fatal("previous operation's email accepted")
	}
	if _, e := ExtractRegistration([]byte(raw), "test@outlook.com", now.Add(-time.Second), now.Add(61*time.Minute)); e == nil {
		t.Fatal("expired verification accepted")
	}
}
