package eva

import (
	"awarddesk/internal/antibot"
	"context"
	"errors"
)

// The solver uses its own HTTP client; only prepare touches the airline cookie
// jar. Both tasks finish before the token check and availability POST.
func bookingVerification(ctx context.Context, solve func(context.Context) (antibot.CaptchaSolution, error), prepare func(context.Context) error) (antibot.CaptchaSolution, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type answer struct {
		solution antibot.CaptchaSolution
		err      error
	}
	answers := make(chan answer, 1)
	go func() {
		solution, err := solve(ctx)
		if err != nil {
			cancel()
		}
		answers <- answer{solution, err}
	}()
	err := prepare(ctx)
	if err != nil {
		cancel()
	}
	a := <-answers
	if err != nil && !(errors.Is(err, context.Canceled) && a.err != nil) {
		return antibot.CaptchaSolution{}, err
	}
	return a.solution, a.err
}
