package antibot

import (
	"awarddesk/internal/session"
	"context"
	"encoding/base64"
	"errors"
	"regexp"
	"time"
)

// SolveText sends only the CAPTCHA image, never the surrounding login form.
func (a AnySolver) SolveText(ctx context.Context, page string, image []byte) (string, error) {
	if len(image) == 0 || len(image) > 512*1024 {
		return "", errors.New("CAPTCHA image has invalid size")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	value, err := a.call(ctx, "createTask", map[string]any{
		"task":     map[string]any{"type": "TextRecognition", "websiteURL": page, "imageBase64": base64.StdEncoding.EncodeToString(image)},
		"settings": map[string]any{"routing": map[string]any{"mode": "priority", "providerOrder": []string{"CapSolver", "CapMonster"}}, "retry": map[string]any{"enabled": false}, "fallback": map[string]any{"enabled": true, "maxFallbacks": 1}},
	})
	if err != nil {
		return "", err
	}
	id, _ := value["taskId"].(string)
	for {
		if value["status"] == "ready" {
			s, _ := value["solution"].(map[string]any)
			text, _ := s["text"].(string)
			if !regexp.MustCompile(`^[A-Za-z0-9]{4,12}$`).MatchString(text) {
				return "", errors.New("CAPTCHA recognition returned an invalid code")
			}
			return text, nil
		}
		if id == "" {
			return "", errors.New("CAPTCHA recognition task ID missing")
		}
		if value["status"] == "failed" {
			return "", errors.New("CAPTCHA image could not be recognized")
		}
		if err = session.Pause(ctx, 2*time.Second); err != nil {
			return "", err
		}
		value, err = a.call(ctx, "getTaskResult", map[string]any{"taskId": id})
		if err != nil {
			return "", err
		}
	}
}
