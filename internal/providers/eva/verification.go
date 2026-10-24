package eva

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"

	http "github.com/bogdanfinn/fhttp"

	"awarddesk/internal/antibot"
	"awarddesk/internal/award"
)

type bookingRecaptchaResult struct{ Success, Source string }

func checkBookingRecaptcha(ctx context.Context, c pricingClient, target, token string) (bookingRecaptchaResult, error) {
	var data bytes.Buffer
	writer := multipart.NewWriter(&data)
	if err := writer.WriteField("token", token); err != nil {
		return bookingRecaptchaResult{}, err
	}
	if err := writer.Close(); err != nil {
		return bookingRecaptchaResult{}, err
	}
	_, check, err := c.Do(ctx, "POST", "https://booking.evaair.com/flyeva/handler/CheckRecaptcha.ashx", &data, http.Header{"sec-fetch-site": {"same-origin"}, "content-type": {writer.FormDataContentType()}, "referer": {target}, "origin": {"https://booking.evaair.com"}, "sec-fetch-mode": {"cors"}, "sec-fetch-dest": {"empty"}})
	if err != nil {
		return bookingRecaptchaResult{}, err
	}
	var verified bookingRecaptchaResult
	if json.Unmarshal(check, &verified) != nil || verified.Source == "" || (verified.Success != "true" && verified.Success != "false") {
		return bookingRecaptchaResult{}, award.Failure("captcha_changed", "EVA booking CAPTCHA verification format changed")
	}
	return verified, nil
}

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
