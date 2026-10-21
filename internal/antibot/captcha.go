package antibot

import (
	"awarddesk/internal/award"
	"awarddesk/internal/session"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"
)

// AnySolver calls the solver API directly. When Proxy is configured, the
// solving task uses the same sticky proxy as the airline session. Without a
// proxy the solver uses its own egress, which may differ from the airline client.
type AnySolver struct {
	Key       string
	Proxy     string
	Client    *http.Client
	UserAgent string
}

type transientSolverError struct{ message string }

func (e *transientSolverError) Error() string { return e.message }

func (a AnySolver) call(ctx context.Context, path string, p map[string]any) (map[string]any, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, err := a.callOnce(ctx, path, p)
		var temporary *transientSolverError
		if err == nil || path != "getTaskResult" || attempt >= 1 || !errors.As(err, &temporary) || ctx.Err() != nil {
			return value, err
		}
		session.ReportRetry(ctx, award.Failure("captcha_service", temporary.Error()), attempt+1)
		if err = session.Pause(ctx, 2*time.Second); err != nil {
			return nil, err
		}
	}
}

func (a AnySolver) callOnce(ctx context.Context, path string, p map[string]any) (map[string]any, error) {
	p["clientKey"] = a.Key
	b, e := json.Marshal(p)
	if e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", "https://api.anysolver.com/"+path, bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	c := a.Client
	if c == nil {
		c = &http.Client{Timeout: 35 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res, e := c.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &transientSolverError{"CAPTCHA service connection failed"}
	}
	defer res.Body.Close()
	b, e = io.ReadAll(io.LimitReader(res.Body, 1024*1024))
	if e != nil {
		return nil, &transientSolverError{"CAPTCHA response could not be read"}
	}
	if res.StatusCode >= 500 {
		return nil, &transientSolverError{"CAPTCHA service temporarily unavailable"}
	}
	var v map[string]any
	if res.StatusCode != 200 || json.Unmarshal(b, &v) != nil {
		return nil, errors.New("CAPTCHA service returned an invalid response")
	}
	if id, ok := v["errorId"].(float64); !ok || id != 0 {
		// Use fixed messages only: upstream descriptions can echo proxy or
		// account credentials. Retain actionable distinctions for the UI.
		switch v["errorCode"] {
		case "PROXY_INCOMPATIBLE":
			return nil, errors.New("CAPTCHA provider cannot use this proxy configuration")
		case "ERROR_ZERO_BALANCE":
			return nil, errors.New("CAPTCHA service balance is exhausted")
		case "ERROR_KEY_DENIED_ACCESS":
			return nil, errors.New("CAPTCHA API key was rejected")
		}
		if code, ok := v["errorCode"].(string); ok && regexp.MustCompile(`^[A-Z][A-Z_]{2,63}$`).MatchString(code) {
			return nil, errors.New("CAPTCHA service rejected the task: " + code)
		}
		return nil, errors.New("CAPTCHA service rejected the task")
	}
	return v, nil
}
func (a AnySolver) Solve(ctx context.Context, page, key string) (CaptchaSolution, error) {
	if a.Key == "" {
		return CaptchaSolution{}, errors.New("AnySolver API key is not configured")
	}
	proxy, e := session.ParseProxy(a.Proxy)
	if e != nil {
		return CaptchaSolution{}, errors.New("CAPTCHA HTTP proxy configuration is invalid")
	}
	userAgent := a.UserAgent
	if userAgent == "" {
		userAgent = session.UserAgent
	}
	task := map[string]any{"type": "PopularCaptchaTokenProxyLess", "websiteURL": page, "websiteKey": key, "userAgent": userAgent}
	if proxy != "" {
		task["type"] = "PopularCaptchaToken"
		task["proxy"] = proxy
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	settings := map[string]any{"routing": map[string]any{"mode": "autoCheapest"}, "retry": map[string]any{"enabled": true, "maxRetries": 1}, "fallback": map[string]any{"enabled": true, "maxFallbacks": 1}}
	if proxy != "" {
		// A single sticky endpoint intentionally remains the same on a solver
		// retry; rotating it would break its association with the BA session.
		settings["allowProxyReuse"] = true
	}
	v, e := a.call(ctx, "createTask", map[string]any{"task": task, "settings": settings})
	if e != nil {
		return CaptchaSolution{}, e
	}
	id, _ := v["taskId"].(string)
	if id == "" {
		return CaptchaSolution{}, errors.New("CAPTCHA service omitted task identifier")
	}
	for {
		if e = session.Pause(ctx, 5*time.Second); e != nil {
			return CaptchaSolution{}, e
		}
		v, e = a.call(ctx, "getTaskResult", map[string]any{"taskId": id})
		if e != nil {
			return CaptchaSolution{}, e
		}
		if v["status"] == "processing" {
			continue
		}
		if v["status"] != "ready" {
			return CaptchaSolution{}, errors.New("CAPTCHA task failed")
		}
		return decodeCaptchaSolution(v["solution"])
	}
}

func decodeCaptchaSolution(value any) (CaptchaSolution, error) {
	solution, _ := value.(map[string]any)
	token, _ := solution["token"].(string)
	if token == "" {
		return CaptchaSolution{}, errors.New("CAPTCHA token missing")
	}
	userAgent, _ := solution["userAgent"].(string)
	return CaptchaSolution{Token: token, UserAgent: userAgent}, nil
}
