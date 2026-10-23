package eva

import (
	"context"

	"awarddesk/internal/award"
)

func (p *Provider) refreshAwardChoices(ctx context.Context, q award.Query, nav awardNavigation, available map[string]bool, from string, rebuild *bool) ([]byte, string, []awardChoice, error) {
	if !*rebuild {
		body, target, err := nav(ctx, "GET", awardJourneyURL, nil, from)
		if err != nil {
			return nil, target, nil, err
		}
		choices, err := parseAwardChoices(body, q, available)
		if err == nil {
			return body, target, choices, nil
		}
		*rebuild = true
	}
	body, target, err := p.loadAwardJourneys(ctx, q, nav)
	if err != nil {
		return nil, target, nil, err
	}
	choices, err := parseAwardChoices(body, q, available)
	return body, target, choices, err
}
