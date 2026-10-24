package antibot

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
)

func TestRecaptchaScoreDefaultsAndConfirmationOverride(t *testing.T) {
	for _, score := range []float64{0, 0.9} {
		calls := 0
		solver := AnySolver{Key: "test", RecaptchaMinScore: score, Client: &http.Client{Transport: solverTransportFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			var payload struct {
				Task struct {
					MinScore   float64 `json:"minScore"`
					PageAction string  `json:"pageAction"`
				} `json:"task"`
				Settings struct {
					Routing struct {
						ProviderOrder []string `json:"providerOrder"`
					} `json:"routing"`
				} `json:"settings"`
			}
			if json.NewDecoder(r.Body).Decode(&payload) != nil {
				t.Fatal("invalid solver payload")
			}
			want := score
			if want == 0 {
				want = 0.7
			}
			if payload.Task.MinScore != want || payload.Task.PageAction != "BookingOnline" {
				t.Fatal("solver score or action changed")
			}
			provider := "CapSolver"
			if score == 0.9 {
				provider = "TwoCaptcha"
			}
			if len(payload.Settings.Routing.ProviderOrder) != 2 || payload.Settings.Routing.ProviderOrder[0] != provider {
				t.Fatal("CAPTCHA provider preference was lost")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"errorId":0}`))}, nil
		})}}
		if score == 0.9 {
			solver.RecaptchaProviderOrder = []string{"TwoCaptcha", "CapSolver"}
		}
		_, err := solver.SolveRecaptchaV3(context.Background(), "https://booking.evaair.com/", "public-site-key", "BookingOnline", "Booking")
		if err == nil || calls != 1 {
			t.Fatal("expected incomplete fake task after exactly one submission")
		}
	}
}

func TestRecaptchaInvalidScoreDoesNotCreateTask(t *testing.T) {
	for _, score := range []float64{-1, 1, math.NaN(), math.Inf(1)} {
		solver := AnySolver{Key: "test", RecaptchaMinScore: score, Client: &http.Client{Transport: solverTransportFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid score made a paid task")
			return nil, nil
		})}}
		if _, err := solver.SolveRecaptchaV3(context.Background(), "https://booking.evaair.com/", "key", "BookingOnline", "Booking"); err == nil {
			t.Fatal("invalid score accepted")
		}
	}
}
