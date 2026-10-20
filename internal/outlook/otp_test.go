package outlook

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestOnlyFreshMatchingOTP(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC)
	message := fmt.Sprintf("From: %s\r\nTo: test@example.test\r\nSubject: %s\r\nDate: %s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n<p>Your one-time password (OTP) is: 123456.</p>", sender, subject, now.Format(time.RFC1123Z))
	code, err := Extract([]byte(message), "test@example.test", now.Add(-time.Second), now)
	if err != nil || code != "123456" {
		t.Fatal("fresh OTP failed", err)
	}
	for _, tc := range []struct {
		name, raw, recipient string
		since, at            time.Time
	}{
		{"wrong recipient", message, "other@example.test", now, now},
		{"wrong sender", strings.Replace(message, sender, "fake@example.test", 1), "test@example.test", now, now},
		{"wrong subject", strings.Replace(message, subject, "Other code", 1), "test@example.test", now, now},
		{"expired", message, "test@example.test", now, now.Add(4 * time.Minute)},
		{"old operation", message, "test@example.test", now.Add(time.Minute), now.Add(time.Minute)},
		{"ambiguous", message + " Your one-time password (OTP) is: 654321.", "test@example.test", now, now},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Extract([]byte(tc.raw), tc.recipient, tc.since, tc.at); err == nil {
				t.Fatal("unsafe OTP accepted")
			}
		})
	}
}
