package antibot

import (
	"context"
	"errors"
	"log"
	"regexp"
	"time"

	"awarddesk/internal/session"
)

func (a AnySolver) SolveRecaptchaV3(ctx context.Context, page, key, action, title string) (CaptchaSolution, error) {
	if a.Key == "" {
		return CaptchaSolution{}, errors.New("AnySolver API key is not configured")
	}
	proxy, err := session.ParseProxy(a.Proxy)
	if err != nil {
		return CaptchaSolution{}, errors.New("CAPTCHA proxy configuration invalid")
	}
	task := map[string]any{"type": "ReCaptchaV3TokenProxyLess", "websiteURL": page, "websiteKey": key, "pageAction": action, "pageTitle": title, "apiDomain": "recaptcha.net", "minScore": 0.7}
	if proxy != "" {
		task["type"] = "ReCaptchaV3Token"
		task["proxy"] = proxy
	}
	ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	settings := map[string]any{
		"routing":         map[string]any{"mode": "priority", "providerOrder": []string{"CapSolver", "TwoCaptcha"}},
		"retry":           map[string]any{"enabled": false, "maxRetries": 0},
		"fallback":        map[string]any{"enabled": true, "maxFallbacks": 1},
		"allowProxyReuse": true,
	}
	value, err := a.call(ctx, "createTask", map[string]any{"task": task, "settings": settings})
	if err != nil {
		return CaptchaSolution{}, err
	}
	id, _ := value["taskId"].(string)
	if id == "" {
		return CaptchaSolution{}, errors.New("CAPTCHA task ID missing")
	}
	lastProvider := ""
	for {
		if err = session.Pause(ctx, 3*time.Second); err != nil {
			return CaptchaSolution{}, err
		}
		value, err = a.call(ctx, "getTaskResult", map[string]any{"taskId": id})
		if err != nil {
			return CaptchaSolution{}, err
		}
		if provider, ok := value["provider"].(string); ok && provider != lastProvider && regexp.MustCompile(`^[A-Za-z]{3,24}$`).MatchString(provider) {
			log.Printf("reCAPTCHA solver provider: %s", provider)
			lastProvider = provider
		}
		if value["status"] == "processing" {
			continue
		}
		if value["status"] != "ready" {
			return CaptchaSolution{}, errors.New("reCAPTCHA task failed")
		}
		return decodeCaptchaSolution(value["solution"])
	}
}
