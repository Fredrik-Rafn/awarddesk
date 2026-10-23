package eva

import (
	"context"
	"net/url"
	"strings"

	"awarddesk/internal/award"
)

type awardSelectionPreparation func(context.Context, string, []byte, int) error

func withAwardSelectionSecurity(nav awardNavigation, prepare awardSelectionPreparation) awardNavigation {
	var body []byte
	var page, action string
	return func(ctx context.Context, method, target string, values url.Values, from string) ([]byte, string, error) {
		confirm := false
		for name, v := range values {
			confirm = confirm || (strings.HasSuffix(name, "__MasterEVENTTARGET") && len(v) == 1 && v[0] == "btn_Confirm_Click")
		}
		if method == "POST" && allowedAwardPage(target, "select-first-journey.aspx") && confirm {
			if len(body) == 0 || target != action || from != page {
				return nil, target, award.Failure("award_protocol", "EVA award selection security page is no longer current")
			}
			freshBody, freshPage := body, page
			body, page, action = nil, "", ""
			if err := prepare(ctx, freshPage, freshBody, 3); err != nil {
				return nil, target, err
			}
		}

		body, page, action = nil, "", ""
		result, next, err := nav(ctx, method, target, values, from)
		if err == nil && allowedAwardPage(next, "select-first-journey.aspx") {
			selection, parseErr := parseForm(result, next)
			if parseErr == nil && allowedAwardPage(selection.action, "select-first-journey.aspx") && selection.field("$hiddenSelectValue") != "" {
				body, page, action = append([]byte(nil), result...), next, selection.action
			}
		}
		return result, next, err
	}
}
