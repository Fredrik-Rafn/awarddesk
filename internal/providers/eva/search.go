package eva

import (
	"awarddesk/internal/antibot"
	"awarddesk/internal/award"
	"awarddesk/internal/session"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime/multipart"
	"net/url"
	"strconv"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

const BookingURL = "https://booking.evaair.com/flyeva/eva/b2c/booking-online.aspx?lang=en-global"
const overrideURL = "https://book.evaair.com/plnext/EVAOnlineDX/Override.action"

func isReeseContinuation(body []byte, target string) bool {
	if !bytes.Contains(body, []byte("reese84-resubmit-data")) {
		return false
	}
	f, err := parseForm(body, target)
	return err == nil && f.action == overrideURL && f.values.Get("reese84-resubmit-data") != ""
}

func (p *Provider) Capabilities() award.Capabilities {
	return award.Capabilities{ID: "eva", Name: "EVA Air · Miles & Cash", Program: "Infinity MileageLands", Cabins: []award.Cabin{award.Economy, award.PremiumEconomy, award.Business}, VerifiedCabins: []award.Cabin{award.Economy, award.PremiumEconomy, award.Business}, MaxAdults: 1, OneWayOnly: true}
}
func (p *Provider) Search(ctx context.Context, q award.Query, report award.Reporter) (award.Result, error) {
	return p.searchCached(ctx, q, report, p.searchLive)
}

func (p *Provider) searchLive(ctx context.Context, q award.Query, report award.Reporter) (award.Result, error) {
	result := award.Result{Query: q}
	if err := q.Validate(time.Now()); err != nil {
		return result, err
	}
	if q.Provider != "eva" || q.Cabin == award.First {
		return result, award.Failure("unsupported", "EVA search supports economy, premium economy and business")
	}
	if q.Adults != 1 {
		return result, award.Failure("unsupported", "EVA pricing uses the authenticated member profile for one adult")
	}
	stage := "session preparation"
	stageStarted := time.Now()
	defer func() {
		slog.Info("EVA search stage", "stage", stage, "elapsed", time.Since(stageStarted).Round(time.Millisecond))
	}()
	observer := report
	report = func(p award.Progress) {
		if p.Stage != stage {
			slog.Info("EVA search stage", "stage", stage, "elapsed", time.Since(stageStarted).Round(time.Millisecond))
			stageStarted = time.Now()
		}
		stage = p.Stage
		if observer != nil {
			observer(p)
		}
	}
	err := p.withSession(ctx, report, func(c *session.Client, proxy string) (useErr error) {
		defer func() {
			if canRenewSearch(stage, useErr) {
				useErr = &searchRenewal{cause: useErr}
			}
		}()
		attempt := func() error {
			progress := func(stage, message string) {
				if report != nil {
					report(award.Progress{Stage: stage, Message: message})
				}
			}
			var body []byte
			var target string
			var sso form
			var err error
			for attempt := 0; attempt < 2; attempt++ {
				progress("flight search", "Opening EVA Miles & Cash search with the saved member session")
				body, target, err = navigate(ctx, c, "GET", BookingURL, nil)
				if err != nil {
					return err
				}
				if isAdaptiveBookingChallenge(body, target) {
					if err = p.clearAvailabilityChallenge(ctx, c, body, target, attempt, progress); err != nil {
						return err
					}
					continue
				}
				f, err := parseForm(body, target)
				if err != nil {
					return award.Failure("search_protocol", "EVA search form was not found")
				}
				if f.field("$txt_From") == "" {
					return award.Failure("search_protocol", "EVA did not return the booking form")
				}
				bookingBody := body
				progress("booking form security", "Preparing EVA security and CAPTCHA together")
				if f.key == "" || f.captchaAction == "" {
					return award.Failure("captcha_changed", "EVA booking CAPTCHA configuration missing")
				}
				userAgent := c.UserAgent()
				solution, err := bookingVerification(ctx, func(ctx context.Context) (antibot.CaptchaSolution, error) {
					return (antibot.AnySolver{Key: p.config.AnyKey, Proxy: proxy, UserAgent: userAgent}).SolveRecaptchaV3(ctx, target, f.key, f.captchaAction, f.title)
				}, func(ctx context.Context) error {
					return (antibot.Hyper{Key: p.config.HyperKey, SkipPixel: true, VerifyAtLogin: true, MinimumSensorRounds: 3}).Prepare(ctx, c, target, bookingBody)
				})
				if err != nil {
					return err
				}
				var data bytes.Buffer
				writer := multipart.NewWriter(&data)
				if err = writer.WriteField("token", solution.Token); err != nil {
					return err
				}
				if err = writer.Close(); err != nil {
					return err
				}
				_, check, err := c.Do(ctx, "POST", "https://booking.evaair.com/flyeva/handler/CheckRecaptcha.ashx", &data, http.Header{"sec-fetch-site": {"same-origin"}, "content-type": {writer.FormDataContentType()}, "referer": {target}, "origin": {"https://booking.evaair.com"}, "sec-fetch-mode": {"cors"}, "sec-fetch-dest": {"empty"}})
				if err != nil {
					return err
				}
				var verified struct {
					Success string
					Source  string
				}
				if json.Unmarshal(check, &verified) != nil || verified.Source == "" || (verified.Success != "true" && verified.Success != "false") {
					return award.Failure("captcha_changed", "EVA booking CAPTCHA verification format changed")
				}
				if err = fillSearch(&f, q, verified.Success, verified.Source); err != nil {
					return err
				}
				// A saved member session can retain a stop marker from an earlier
				// protected action. EVA's booking form needs the complete new sensor
				// sequence: the one-sensor shortcut returned 403 on repeated searches.
				progress("availability", "Requesting all flight options for the selected date")
				body, target, err = navigate(ctx, c, "POST", f.action, f.values, target)
				if err != nil {
					return err
				}
				if isAdaptiveBookingChallenge(body, target) {
					if err = p.clearAvailabilityChallenge(ctx, c, body, target, attempt, progress); err != nil {
						return err
					}
					// The interstitial explicitly intercepted the search. Reopen the
					// form and acquire fresh CAPTCHA and form state, as history.back
					// does in EVA's browser flow. No credentials or pricing are replayed.
					continue
				}
				sso, err = parseForm(body, target)
				if err != nil || sso.action != overrideURL || sso.values.Get("EMBEDDED_TRANSACTION") != "FlexPricerAvailability" || sso.values.Get("ENC") == "" {
					return award.Failure("search_protocol", "EVA did not return the flight-availability handoff")
				}
				break
			}
			// Use the observed availability handoff. Price preparation is handled
			// separately with the authenticated member profile; no payment is sent.
			progress("booking handoff", "Opening EVA's flight availability application")
			body, target, err = navigate(ctx, c, "POST", sso.action, sso.values, target)
			if err != nil {
				return err
			}
			body, target, err = completeBookingSecurity(ctx, body, target, func(ctx context.Context, target string, body []byte) error {
				if handled, e := verifyBookingCaptcha(ctx, c, antibot.AnySolver{Key: p.config.AnyKey, Proxy: proxy, UserAgent: c.UserAgent()}, body, target, progress); handled || e != nil {
					return e
				}
				if p.config.ImpervaSolver == "anysolver" {
					return (antibot.AnySolver{Key: p.config.AnyKey}).PrepareReese(ctx, c, target, body)
				}
				return (antibot.Hyper{Key: p.config.HyperKey}).PrepareReese(ctx, c, target, body)
			}, func(ctx context.Context, target string, values url.Values, from string) ([]byte, string, error) {
				return navigate(ctx, c, "POST", target, values, from)
			}, progress)
			if err != nil {
				return err
			}
			page, err := decodePage(body)
			if err != nil {
				return err
			}
			result, err = parseAvailability(page.Business["Availability"], q)
			if err != nil {
				return err
			}
			return priceFares(ctx, c, page, &result, report)
		}
		return retrySearchPreparation(ctx, func() (string, error) {
			err := attempt()
			return stage, err
		}, report)
	})
	var failure *award.Error
	if errors.As(err, &failure) && failure.Stage == "" {
		copy := *failure
		copy.Stage = stage
		err = &copy
	}
	return result, err
}

func fillSearch(f *form, q award.Query, captchaResult, captchaData string) error {
	cabin := map[award.Cabin]string{award.Economy: "EY", award.PremiumEconomy: "ED", award.Business: "SD"}[q.Cabin]
	if cabin == "" {
		return award.Failure("unsupported", "EVA cabin unsupported")
	}
	fields := map[string]string{
		"$txt_From": q.Origin, "$txt_To": q.Destination, "$txt_date": q.Date, "$txt_date1": "", "$txt_date2": "",
		"$rbl_DisplayType": "2", "$ddl_cabinclass": cabin, "$txt_Adult": strconv.Itoa(q.Adults), "$txt_Child": "0", "$txt_Infant": "0",
		"$hid_Segment": "1", "$hi_RecaptchaResult": captchaResult, "$hi_RecaptchaData": captchaData,
		"__MasterEVENTTARGET": "ctl00$content$btn_ok", "__MasterEVENTARGUMENT": "",
	}
	for suffix, value := range fields {
		if err := f.set(suffix, value); err != nil {
			return award.Failure("search_protocol", "EVA search fields changed")
		}
	}
	// Unchecked controls are not successful HTML inputs, but this flow
	// explicitly selects Miles & Cash.
	prefix := strings.TrimSuffix(f.field("$txt_From"), "txt_From")
	f.values.Set(prefix+"cbs_MNC", "True")
	return nil
}
