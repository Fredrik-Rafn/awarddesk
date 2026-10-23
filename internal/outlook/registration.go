package outlook

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-imap"
)

const registrationSubject = "EVA Air Infinity MileageLands- Verification Email"

type RegistrationMessage struct {
	Link string `json:"link"`
	Code string `json:"code"`
}

var registrationCode = regexp.MustCompile(`(?i)verification\s+code\s*:\s*([a-z0-9]{6})\b`)
var messageURL = regexp.MustCompile(`https://eservice\.evaair\.com/[^\s<>"']+`)

func ExtractRegistration(raw []byte, recipient string, since, now time.Time) (RegistrationMessage, error) {
	body, err := extractMessageBody(raw, recipient, since, now, registrationSubject, time.Hour)
	if err != nil {
		return RegistrationMessage{}, err
	}
	text := html.UnescapeString(tags.ReplaceAllString(body, " "))
	codes := registrationCode.FindAllStringSubmatch(text, -1)
	if len(codes) == 0 {
		return RegistrationMessage{}, errors.New("registration code missing")
	}
	result := RegistrationMessage{Code: codes[0][1]}
	for _, code := range codes {
		if code[1] != result.Code {
			return RegistrationMessage{}, errors.New("ambiguous registration codes")
		}
	}
	for _, rawLink := range messageURL.FindAllString(html.UnescapeString(body), -1) {
		u, e := url.Parse(rawLink)
		if e != nil || u.User != nil || u.Host != "eservice.evaair.com" || !strings.EqualFold(u.Path, "/flyeva/EVA/FFP/register-now.aspx") || u.RawQuery == "" || u.Fragment != "" || len(rawLink) > 8192 {
			continue
		}
		if result.Link != "" && result.Link != u.String() {
			return RegistrationMessage{}, errors.New("ambiguous registration links")
		}
		result.Link = u.String()
	}
	if result.Link == "" {
		return RegistrationMessage{}, errors.New("registration link missing")
	}
	return result, nil
}

func (r *Reader) WaitRegistration(ctx context.Context, point Checkpoint) (RegistrationMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	points, err := retryMailboxRead(ctx, func() ([]Checkpoint, error) { return r.registrationFolders(ctx, point) })
	if err != nil {
		return RegistrationMessage{}, err
	}
	extract := func(raw []byte, recipient string, since, now time.Time) (string, error) {
		message, e := ExtractRegistration(raw, recipient, since, now)
		if e != nil {
			return "", e
		}
		b, e := json.Marshal(message)
		return string(b), e
	}
	for {
		for _, mailboxPoint := range points {
			value, err := retryMailboxRead(ctx, func() (string, error) {
				return r.pollMessage(ctx, mailboxPoint, registrationSubject, time.Hour, extract)
			})
			if err != nil {
				return RegistrationMessage{}, err
			}
			if value != "" {
				var message RegistrationMessage
				if json.Unmarshal([]byte(value), &message) != nil {
					return message, errors.New("invalid registration email")
				}
				return message, nil
			}
		}
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return RegistrationMessage{}, errors.New("no fresh EVA registration email arrived before the deadline")
		case <-timer.C:
		}
	}
}

func (r *Reader) registrationFolders(ctx context.Context, point Checkpoint) ([]Checkpoint, error) {
	c, _, cleanup, err := r.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	mailboxes := make(chan *imap.MailboxInfo)
	done := make(chan error, 1)
	go func() { done <- c.List("", "*", mailboxes) }()
	folder := ""
	for box := range mailboxes {
		junk := strings.EqualFold(box.Name, "Junk") || strings.EqualFold(box.Name, "Junk Email")
		for _, attr := range box.Attributes {
			if strings.EqualFold(attr, `\Junk`) {
				junk = true
			}
		}
		if junk {
			folder = box.Name
		}
	}
	if err = <-done; err != nil {
		return nil, mailboxReadError("Outlook folders could not be listed", err)
	}
	points := []Checkpoint{point}
	if folder != "" {
		box, e := c.Select(folder, true)
		if e != nil {
			return nil, mailboxReadError("Outlook junk folder could not be opened read-only", e)
		}
		points = append(points, Checkpoint{Folder: folder, UIDValidity: box.UidValidity, NextUID: 1, Since: point.Since})
	}
	return points, nil
}
