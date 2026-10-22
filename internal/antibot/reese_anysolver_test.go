package antibot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"awarddesk/internal/session"
)

func TestReeseSensorContractAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, ua, payload string
		wantError         bool
	}{
		{"pinned browser", session.UserAgent, `{"sensor":"opaque"}`, false},
		{"changed browser", "different-browser", `{"sensor":"opaque"}`, true},
		{"missing payload", session.UserAgent, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := AnySolver{Key: "test-key", Client: &http.Client{Transport: solverTransportFunc(func(r *http.Request) (*http.Response, error) {
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				task := request["task"].(map[string]any)
				if task["type"] != "ImpervaReese84SensorProxyLess" || task["reeseUrl"] != "https://book.evaair.com/a/b?s=fresh" || task["userAgent"] != session.UserAgent || task["proxy"] != nil {
					t.Fatal("wrong sensor contract")
				}
				body, _ := json.Marshal(map[string]any{"errorId": 0, "status": "ready", "solution": map[string]any{"payload": tc.payload, "userAgent": tc.ua, "acceptLanguage": session.Language}})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}}
			payload, _, err := a.solveReese(context.Background(), "https://book.evaair.com/", "https://book.evaair.com/a/b?s=fresh", session.UserAgent)
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error state: %v", err)
			}
			if err == nil && payload != tc.payload {
				t.Fatal("signed payload modified")
			}
		})
	}
}
