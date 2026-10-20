package antibot

import (
	"awarddesk/internal/award"
	"bytes"
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	hyper "github.com/Hyper-Solutions/hyper-sdk-go/v3"
	"github.com/Hyper-Solutions/hyper-sdk-go/v3/akamai"
	http "github.com/bogdanfinn/fhttp"
)

// initializePixel handles the independent Akamai pixel advertised by a fresh
// page. Its dynamic HTML/script variables must never come from a saved capture.
func (r *hyperSession) initializePixel(page string, body []byte) error {
	if !bytes.Contains(body, []byte("bazadebezolkohpepadr")) {
		return nil
	}
	htmlVar, err := akamai.ParsePixelHtmlVar(bytes.NewReader(body))
	if err != nil {
		return award.Failure("challenge_changed", "Airline pixel HTML changed")
	}
	scriptURL, postURL, err := akamai.ParsePixelScriptURL(bytes.NewReader(body))
	if err != nil {
		return award.Failure("challenge_changed", "Airline pixel script URL changed")
	}
	scriptURL, origin, err := challengeEndpoint(page, scriptURL)
	if err != nil {
		return err
	}
	postURL, _, err = challengeEndpoint(page, postURL)
	if err != nil {
		return err
	}
	_, script, err := r.request("GET", scriptURL, nil, http.Header{
		"referer": {page}, "sec-fetch-mode": {"no-cors"}, "sec-fetch-site": {"same-origin"}, "sec-fetch-dest": {"script"},
	})
	if err != nil {
		return err
	}
	scriptVar, err := akamai.ParsePixelScriptVar(bytes.NewReader(script))
	if err != nil {
		return award.Failure("challenge_changed", "Airline pixel script changed")
	}
	ip, err := r.outboundIP()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.ctx, 45*time.Second)
	defer cancel()
	payload, err := hyper.NewSession(r.key).GeneratePixelData(ctx, &hyper.PixelInput{
		UserAgent: r.client.UserAgent(), HTMLVar: strconv.Itoa(htmlVar), ScriptVar: scriptVar, AcceptLanguage: lang, IP: ip,
	})
	if err != nil {
		return award.Failure("challenge_service", "Hyper could not generate the pixel response")
	}
	_, _, err = r.request("POST", postURL, strings.NewReader(payload), http.Header{
		"content-type": {"application/x-www-form-urlencoded"}, "accept": {"*/*"},
		"origin": {origin}, "referer": {page},
		"sec-fetch-mode": {"cors"}, "sec-fetch-site": {"same-origin"}, "sec-fetch-dest": {"empty"},
	})
	if err == nil {
		log.Print("Akamai pixel submission completed; application access still unverified")
	}
	return err
}
