package antibot

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/Hyper-Solutions/hyper-sdk-go/v3/incapsula"
	http "github.com/bogdanfinn/fhttp"

	"awarddesk/internal/award"
	"awarddesk/internal/session"
)

func (a AnySolver) PrepareReese(ctx context.Context, c *session.Client, page string, body []byte) (resultErr error) {
	started := time.Now()
	defer func() {
		slog.Info("EVA Reese84 verification", "solver", "anysolver", "success", resultErr == nil, "elapsedMs", time.Since(started).Milliseconds())
	}()
	sensorPath, scriptPath, err := incapsula.ParseDynamicReeseScript(bytes.NewReader(body), page)
	if err != nil {
		return award.Failure("challenge_changed", "EVA booking security page was not recognized")
	}
	scriptURL, _, err := challengeEndpoint(page, scriptPath)
	if err != nil {
		return err
	}
	sensorURL, origin, err := challengeEndpoint(page, sensorPath)
	if err != nil {
		return err
	}

	if _, _, err = c.Do(ctx, "GET", scriptURL, nil, http.Header{"sec-fetch-site": {"same-origin"}, "referer": {page}, "sec-fetch-mode": {"no-cors"}, "sec-fetch-dest": {"script"}}); err != nil {
		return err
	}
	payload, language, err := a.solveReese(ctx, page, scriptURL, c.UserAgent())
	if err != nil {
		return err
	}
	return submitReese(ctx, c, page, sensorURL, origin, payload, language)
}

func (a AnySolver) solveReese(ctx context.Context, page, script, userAgent string) (string, string, error) {
	if a.Key == "" {
		return "", "", award.Failure("configuration", "AnySolver API key is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	v, err := a.call(ctx, "createTask", map[string]any{
		"task":     map[string]any{"type": "ImpervaReese84SensorProxyLess", "websiteURL": page, "reeseUrl": script, "userAgent": userAgent},
		"settings": map[string]any{"routing": map[string]any{"mode": "priority", "providerOrder": []string{"Salamoonder"}}, "retry": map[string]any{"enabled": false, "maxRetries": 0}, "fallback": map[string]any{"enabled": false, "maxFallbacks": 0}},
	})
	if err != nil {
		return "", "", err
	}
	id, _ := v["taskId"].(string)
	for {
		if v["status"] == "ready" {
			break
		}
		if v["status"] != nil && v["status"] != "processing" {
			return "", "", award.Failure("challenge_service", "AnySolver Reese84 task failed")
		}
		if id == "" {
			return "", "", award.Failure("challenge_service", "AnySolver omitted the Reese84 task identifier")
		}
		if err = session.Pause(ctx, 2*time.Second); err != nil {
			return "", "", err
		}
		v, err = a.call(ctx, "getTaskResult", map[string]any{"taskId": id})
		if err != nil {
			return "", "", err
		}
	}
	solution, _ := v["solution"].(map[string]any)
	payload, _ := solution["payload"].(string)
	ua, _ := solution["userAgent"].(string)
	language, _ := solution["acceptLanguage"].(string)
	if payload == "" || len(payload) > 1024*1024 {
		return "", "", award.Failure("challenge_service", "AnySolver Reese84 payload missing or oversized")
	}
	if ua != "" && ua != userAgent {
		return "", "", award.Failure("challenge_identity", "AnySolver Reese84 browser identity differs from the saved EVA session")
	}
	if language == "" {
		language = session.Language
	}
	if len(language) > 128 || strings.ContainsAny(language, "\r\n") {
		return "", "", award.Failure("challenge_identity", "AnySolver Reese84 language header is invalid")
	}
	return payload, language, nil
}
