package eva

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strings"

	http "github.com/bogdanfinn/fhttp"

	"awarddesk/internal/award"
	"awarddesk/internal/session"
)

func navigate(ctx context.Context, c *session.Client, method, target string, values url.Values, from ...string) ([]byte, string, error) {
	previous := LoginURL
	if len(from) > 0 {
		previous = from[0]
	} else if method == "GET" && target == LoginURL {
		previous = ""
	}
	for i := 0; i < 6; i++ {
		u, err := url.Parse(target)
		if err != nil || u.Scheme != "https" || u.User != nil || (u.Host != "booking.evaair.com" && u.Host != "eservice.evaair.com" && u.Host != "book.evaair.com") {
			return nil, "", award.Failure("unsafe_destination", "EVA navigation left the allowed portal")
		}
		var body io.Reader
		h := http.Header{"sec-fetch-site": {"none"}}
		var source *url.URL
		if previous != "" {
			source, err = url.Parse(previous)
			if err != nil || source.Scheme != "https" || !strings.HasSuffix(source.Hostname(), ".evaair.com") {
				return nil, target, award.Failure("unsafe_destination", "Invalid EVA referring page")
			}
			h["referer"] = []string{previous}
			h["sec-fetch-site"] = []string{"same-site"}
			if source.Host == u.Host {
				h["sec-fetch-site"] = []string{"same-origin"}
			}
		}
		if method == "POST" {
			body = strings.NewReader(values.Encode())
			h["content-type"] = []string{"application/x-www-form-urlencoded"}
			if source == nil {
				return nil, target, award.Failure("unsafe_destination", "EVA form origin missing")
			}
			h["origin"] = []string{source.Scheme + "://" + source.Host}
		}
		var res *http.Response
		var data []byte
		res, data, err = c.Do(ctx, method, target, body, h)
		if err != nil {
			if res != nil && awardForwardResponse(res.StatusCode, target, data) {
				return data, target, nil
			}
			if res != nil && (res.StatusCode == 403 || res.StatusCode == 428) && u.Host == "eservice.evaair.com" && (strings.EqualFold(u.Path, "/flyeva/eva/ffp/login.aspx") || strings.EqualFold(u.Path, "/flyeva/EVA/FFP/register-now.aspx")) && bytes.Contains(data, []byte(`name="sec_chlge_forward_wrap"`)) {
				return data, target, nil
			}
			if res != nil && (res.StatusCode == 403 || res.StatusCode == 428) && isAdaptiveBookingChallenge(data, target) {
				return data, target, nil
			}
			if res != nil && res.StatusCode == 403 && u.Host == "book.evaair.com" && isReeseContinuation(data, target) {
				return data, target, nil
			}
			if res != nil && res.StatusCode == 403 {
				kind := "security"
				if bytes.Contains(data, []byte("sec_chlge_forward_wrap")) {
					kind = "additional Akamai challenge"
				} else if bytes.Contains(data, []byte("Access Denied")) {
					kind = "access policy"
				}
				return nil, target, &award.Error{Code: "session_denied", HTTPStatus: 403, Message: "EVA " + u.Host + " rejected the request (HTTP 403; " + kind + ")"}
			}
			return nil, target, err
		}
		if res.StatusCode == 200 {
			return data, target, nil
		}
		if res.StatusCode != 301 && res.StatusCode != 302 && res.StatusCode != 303 {
			return nil, target, award.Failure("login_http", "EVA returned an unexpected navigation response")
		}
		ref, err := url.Parse(res.Header.Get("Location"))
		if err != nil || ref.String() == "" {
			return nil, target, award.Failure("login_protocol", "EVA redirect missing")
		}
		previous = target
		target = u.ResolveReference(ref).String()
		method = "GET"
		values = nil
	}
	return nil, target, award.Failure("redirect_limit", "EVA redirected too many times")
}
