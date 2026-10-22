package eva

import (
	"bytes"
	"context"
	"net/url"
	"regexp"
	"strings"

	http "github.com/bogdanfinn/fhttp"
	"golang.org/x/net/html"

	"awarddesk/internal/antibot"
	"awarddesk/internal/award"
	"awarddesk/internal/session"
)

func verifyBookingCaptcha(ctx context.Context, c pricingClient, solver antibot.Captcha, body []byte, page string, progress func(string, string)) (bool, error) {
	key, endpoint, found, err := bookingCaptcha(body, page)
	if !found || err != nil {
		return found, err
	}
	progress("booking CAPTCHA", "Solving EVA's additional booking CAPTCHA")
	answer, err := solver.Solve(ctx, page, key)
	if err != nil {
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		return true, award.Failure("captcha_service", err.Error())
	}
	response, _, err := c.Do(ctx, "POST", endpoint, strings.NewReader(url.Values{"g-recaptcha-response": {answer.Token}}.Encode()), http.Header{"content-type": {"application/x-www-form-urlencoded"}, "accept": {"*/*"}, "referer": {page}, "origin": {"https://book.evaair.com"}, "sec-fetch-site": {"same-origin"}, "sec-fetch-mode": {"cors"}, "sec-fetch-dest": {"empty"}})
	if err == nil && (response == nil || response.StatusCode != 200) {
		err = award.Failure("challenge_rejected", "EVA did not accept the additional booking CAPTCHA")
	}
	return true, err
}

func isAdaptiveBookingChallenge(body []byte, page string) bool {
	u, err := url.Parse(page)
	return err == nil && u.Scheme == "https" && u.Host == "booking.evaair.com" && u.Path == "/flyeva/eva/b2c/booking-online.aspx" && bytes.Contains(body, []byte(`id="sec-cpt-if"`)) && bytes.Contains(body, []byte(`provider="adaptive"`))
}

func (p *Provider) clearAvailabilityChallenge(ctx context.Context, c *session.Client, body []byte, target string, attempt int, progress func(string, string)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if attempt >= 1 {
		return award.Failure("challenge_rejected", "EVA repeated its availability security challenge after verification")
	}
	progress("adaptive security", "Completing EVA's availability security check, including its required wait")
	return (antibot.Hyper{Key: p.config.HyperKey}).PrepareAdaptive(ctx, c, target, body)
}

// Imperva may replace its sensor interstitial with an hCaptcha page whose
// title is simply "EVAAir". Parse the fresh challenge and its verification
// endpoint rather than relying on a page title or a captured endpoint token.
func bookingCaptcha(body []byte, page string) (key, endpoint string, found bool, err error) {
	root, e := html.Parse(bytes.NewReader(body))
	if e != nil {
		return "", "", false, e
	}
	count := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			attrs := map[string]string{}
			for _, a := range n.Attr {
				attrs[a.Key] = a.Val
			}
			for _, class := range strings.Fields(attrs["class"]) {
				if class == "h-captcha" {
					count++
					key = attrs["data-sitekey"]
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if count == 0 {
		return "", "", false, nil
	}
	fail := func() (string, string, bool, error) {
		return "", "", true, award.Failure("challenge_changed", "EVA additional booking CAPTCHA format changed")
	}
	if count != 1 || !regexp.MustCompile(`^[A-Za-z0-9_-]{20,100}$`).MatchString(key) || !bytes.Contains(body, []byte("g-recaptcha-response=")) {
		return fail()
	}
	matches := regexp.MustCompile(`xhr\.open\(\s*["']POST["']\s*,\s*["']([^"']+)["']`).FindAllSubmatch(body, -1)
	if len(matches) != 1 {
		return fail()
	}
	base, e := url.Parse(page)
	if e != nil || base.Scheme != "https" || base.Host != "book.evaair.com" {
		return fail()
	}
	ref, e := url.Parse(string(matches[0][1]))
	if e != nil {
		return fail()
	}
	u := base.ResolveReference(ref)
	if u.Scheme != "https" || u.Host != base.Host || u.User != nil || u.Fragment != "" || u.Path != "/_Incapsula_Resource" || u.Query().Get("SWCGHOEL") != "v2" || u.Query().Get("dai") == "" || u.Query().Get("cts") == "" {
		return fail()
	}
	return key, u.String(), true, nil
}
