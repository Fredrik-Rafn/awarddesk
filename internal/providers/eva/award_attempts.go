package eva

import (
	"context"
	"errors"
	"time"

	"awarddesk/internal/award"
)

func safeAwardEvidenceSetupFailure(err error, stage string) bool {
	if stage != "award availability" && stage != "award flights" {
		return false
	}
	var failure *award.Error
	if !errors.As(err, &failure) || failure.OutcomeUnknown {
		return false
	}
	if failure.Stage != "" && failure.Stage != "award availability" && failure.Stage != "award flights" {
		return false
	}
	switch failure.Code {
	case "award_protocol", "search_protocol", "connection", "response_read", "challenge_rejected", "site_unavailable":
		return true
	case "session_denied":
		return failure.HTTPStatus == 403
	case "provider_http":
		return failure.HTTPStatus >= 500 && failure.HTTPStatus <= 599
	}
	return false
}

func runAwardAttempts(ctx context.Context, q award.Query, run func(context.Context) (award.Result, error), stage func() string, report award.Reporter) (award.Result, error) {
	for attempt := 0; ; attempt++ {
		_, _, usedProof := usableAwardPartyEvidence(ctx, q, time.Now())
		r, err := run(ctx)
		var failure *award.Error
		if err == nil || attempt == 1 || len(r.Itineraries) > 0 || ctx.Err() != nil || !errors.As(err, &failure) || failure.OutcomeUnknown {
			return r, err
		}
		failedStage := stage()
		proofFallback := usedProof && safeAwardEvidenceSetupFailure(err, failedStage)
		if !proofFallback && failure.Code != "award_availability_changed" {
			return r, err
		}
		if report != nil {
			message := "Award inventory changed between systems; checking fresh availability once"
			if proofFallback {
				message = "Refreshing the helper's availability and itinerary after interrupted direct setup"
			}
			report(award.Progress{Stage: "award availability", Message: message, Recovery: recoveryDetail(failedStage, err, attempt+1)})
		}
		ctx = context.WithValue(ctx, awardPartyEvidenceKey{}, awardPartyEvidence{})
	}
}
