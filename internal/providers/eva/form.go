package eva

import (
	"bytes"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

type form struct {
	values                            url.Values
	action, title, key, captchaAction string
	captchaImage                      string
}

var captchaPattern = regexp.MustCompile(`grecaptcha\.execute\s*\(\s*["']([^"']+)["']\s*,\s*\{\s*action\s*:\s*["']([^"']+)["']`)

func parseForm(body []byte, base string) (form, error) {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return form{}, errors.New("EVA page could not be parsed")
	}
	f := form{values: url.Values{}, action: base}
	found := false
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inForm bool) {
		attrs := map[string]string{}
		for _, a := range n.Attr {
			attrs[a.Key] = a.Val
		}
		if n.Type == html.ElementNode && n.Data == "form" && !found {
			found = true
			inForm = true
			if action := attrs["action"]; action != "" {
				u, _ := url.Parse(base)
				ref, e := url.Parse(action)
				if e == nil {
					f.action = u.ResolveReference(ref).String()
				}
			}
		} else if n.Type == html.ElementNode && n.Data == "form" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "title" && n.FirstChild != nil {
			f.title = n.FirstChild.Data
		}
		if n.Type == html.ElementNode && n.Data == "img" && strings.HasSuffix(attrs["id"], "_CaptchaImage") {
			u, _ := url.Parse(base)
			ref, e := url.Parse(attrs["src"])
			if e == nil && attrs["src"] != "" {
				f.captchaImage = u.ResolveReference(ref).String()
			}
		}
		if inForm && n.Type == html.ElementNode && n.Data == "input" && attrs["name"] != "" {
			_, disabled := attrs["disabled"]
			_, checked := attrs["checked"]
			kind := strings.ToLower(attrs["type"])
			if !disabled && kind != "submit" && kind != "button" && kind != "image" && ((kind != "checkbox" && kind != "radio") || checked) {
				f.values.Add(attrs["name"], attrs["value"])
			}
		}
		if inForm && n.Type == html.ElementNode && n.Data == "select" && attrs["name"] != "" {
			if _, disabled := attrs["disabled"]; !disabled {
				first, selected := "", false
				for option := n.FirstChild; option != nil; option = option.NextSibling {
					if option.Type != html.ElementNode || option.Data != "option" {
						continue
					}
					value, chosen := "", false
					for _, a := range option.Attr {
						if a.Key == "value" {
							value = a.Val
						}
						if a.Key == "selected" {
							chosen = true
						}
					}
					if !selected {
						first = value
						selected = true
					}
					if chosen {
						first = value
						break
					}
				}
				f.values.Set(attrs["name"], first)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inForm)
		}
	}
	walk(root, false)
	if match := captchaPattern.FindSubmatch(body); len(match) == 3 {
		f.key = string(match[1])
		f.captchaAction = string(match[2])
	}
	if !found {
		return f, errors.New("EVA page has no supported form")
	}
	return f, nil
}
func (f form) field(suffix string) string {
	for name := range f.values {
		if strings.HasSuffix(name, suffix) {
			return name
		}
	}
	return ""
}
func (f form) set(suffix, value string) error {
	key := f.field(suffix)
	if key == "" {
		return errors.New("EVA form field changed")
	}
	f.values.Set(key, value)
	return nil
}
