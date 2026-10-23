package fleet

import "awarddesk/internal/award"

func safeAwardFailover(e *award.Error) bool {
	if e.OutcomeUnknown {
		return false
	}

	if e.Stage == "credentials" && (e.Code == "captcha_incorrect" || (e.Code == "session_denied" && e.HTTPStatus == 403)) {
		return true
	}
	if e.Stage == "mailbox" && e.Code == "mailbox" {
		return true
	}
	if safeFailover(e) {
		return true
	}
	switch e.Stage {
	case "award availability", "award security", "award flights", "award pricing", "login page", "captcha", "image captcha", "security", "adaptive security", "proxy verification", "session verification":
	default:
		return false
	}
	switch e.Code {
	case "session_denied", "session_expired", "connection", "timeout", "response_read", "award_availability_changed", "award_protocol", "challenge_rejected":
		return true
	}
	return false
}

func awardQueryFailure(e *award.Error) bool {
	if e.OutcomeUnknown {
		return false
	}
	switch e.Stage {
	case "award availability", "award security", "award flights", "award pricing":
	default:
		return false
	}
	switch e.Code {
	case "award_protocol", "award_availability_changed", "search_mismatch", "unsupported_route", "unsupported", "award_limit":
		return true
	}
	return false
}
