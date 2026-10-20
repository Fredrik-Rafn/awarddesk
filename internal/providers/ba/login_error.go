package ba

import (
	"awarddesk/internal/award"
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// classifyLoginRejection emits only fixed messages. Auth pages can contain
// credentials, tokens and account details, so raw text must not reach the API.
func classifyLoginRejection(body []byte, status int) error {
	doc, _ := html.Parse(bytes.NewReader(body))
	// Ignore script/style dictionaries: merely mentioning a CAPTCHA error in
	// client JavaScript is not evidence that the server rejected the CAPTCHA.
	var visible strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == html.ElementNode {
			for _, a := range n.Attr {
				if a.Key == "hidden" || (a.Key == "aria-hidden" && strings.EqualFold(a.Val, "true")) {
					return
				}
				if a.Key == "class" {
					for _, class := range strings.Fields(a.Val) {
						if class == "hide" || class == "hidden" {
							return
						}
					}
				}
			}
		}
		if n.Type == html.TextNode {
			visible.WriteString(n.Data)
			visible.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	text := strings.ToLower(strings.Join(strings.Fields(visible.String()), " "))
	result := &award.Error{Code: "login_rejected", HTTPStatus: status, Message: "BA rejected the login request before flight search. The response did not identify a recognized cause."}
	for _, rule := range []struct {
		phrases       []string
		code, message string
	}{
		{[]string{"captcha validation failed", "invalid captcha", "captcha verification failed", "captcha check failed", "captcha_required", "captcha_invalid", "invalid-captcha", "captcha failed"}, "login_captcha_rejected", "BA rejected the CAPTCHA verification before login. No flight search was sent."},
		{[]string{"wrong email or password", "wrong username or password", "invalid username or password", "incorrect password"}, "login_credentials_rejected", "BA rejected the account credentials. Check your BA login before trying again."},
		{[]string{"too many attempts", "too many login", "temporarily blocked"}, "login_rate_limited", "BA temporarily limited login attempts. Please wait before trying again."},
		{[]string{"session has expired", "session expired", "invalid state", "invalid_state", "invalid session"}, "login_session_rejected", "BA rejected the fresh login session. No flight search was sent."},
	} {
		for _, phrase := range rule.phrases {
			if strings.Contains(text, phrase) {
				result.Code = rule.code
				result.Message = rule.message
				if rule.code == "login_rate_limited" {
					result.RetryAfterSeconds = 300
				}
				return result
			}
		}
	}
	return result
}
