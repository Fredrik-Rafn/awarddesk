package eva

import (
	"bytes"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// Return only user-facing error-container text, never scripts or form values.
func loginFeedback(body []byte, secrets ...string) string {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	var pieces []string
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inside bool) {
		if n.Type == html.ElementNode {
			if n.Data == "script" || n.Data == "style" || n.Data == "input" {
				return
			}
			for _, a := range n.Attr {
				if a.Key == "id" && (a.Val == "wuc_Error" || strings.HasSuffix(a.Val, "wuc_CertError")) {
					inside = true
				}
			}
		}
		if inside && n.Type == html.TextNode {
			pieces = append(pieces, n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inside)
		}
	}
	walk(root, false)
	text := strings.Join(strings.Fields(strings.Join(pieces, " ")), " ")
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	// Numeric identifiers or OTPs must not be surfaced as diagnostic text.
	text = strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return '#'
		}
		return r
	}, text)
	if len(text) > 350 {
		text = text[:350]
	}
	return text
}
