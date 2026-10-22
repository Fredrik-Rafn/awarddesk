// Package outlook reads fresh EVA verification messages through OAuth2 IMAP.
// It never sends mail, marks messages read, or uses the mailbox password.
package outlook

import (
	"awarddesk/internal/sessionstore"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

const sender = "eservice@mh1.evaair.com"
const subject = "EVA Air Infinity MileageLands- Login Verification Letter"

type Config struct {
	Email, ClientID, RefreshToken string
	StateBackend                  sessionstore.Backend
}
type Reader struct {
	config  Config
	mu      sync.Mutex
	access  string
	expires time.Time
}
type Checkpoint struct {
	Folder               string `json:"folder,omitempty"`
	UIDValidity, NextUID uint32
	Since                time.Time
}

func New(c Config) *Reader { return &Reader{config: c} }
func (r *Reader) token(ctx context.Context) (string, error) {
	return r.refreshAccess(ctx, false)
}
func (r *Reader) refreshAccess(ctx context.Context, force bool) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if force {
		r.access = ""
		r.expires = time.Time{}
	}
	if r.access != "" && time.Until(r.expires) > time.Minute {
		return r.access, nil
	}
	store := r.tokenStore()
	unlock, err := store.Lock()
	if err != nil {
		return "", errors.New("Outlook token store is busy")
	}
	defer unlock()
	refresh, err := r.loadRefreshToken(store)
	if err != nil {
		return "", err
	}
	form := url.Values{"client_id": {r.config.ClientID}, "refresh_token": {refresh}, "grant_type": {"refresh_token"}, "scope": {"https://outlook.office.com/IMAP.AccessAsUser.All offline_access"}}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://login.microsoftonline.com/consumers/oauth2/v2.0/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("could not construct Outlook token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h := &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := h.Do(req)
	if err != nil {
		return "", errors.New("Outlook OAuth connection failed")
	}
	defer res.Body.Close()
	var value struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Expires      int    `json:"expires_in"`
		Error        string `json:"error"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1024*1024)).Decode(&value) != nil {
		return "", errors.New("invalid Outlook OAuth response")
	}
	if res.StatusCode != 200 || value.AccessToken == "" {
		if value.Error == "invalid_grant" {
			return "", errors.New("Outlook refresh token expired or requires renewed consent")
		}
		return "", errors.New("Outlook OAuth authorization failed")
	}
	if value.RefreshToken != "" {
		if err = r.saveRefreshToken(store, value.RefreshToken); err != nil {
			return "", errors.New("could not save rotated Outlook token")
		}
	}
	r.access = value.AccessToken
	r.expires = time.Now().Add(time.Duration(value.Expires) * time.Second)
	return r.access, nil
}

type xoauth struct{ user, token string }

func (x xoauth) Start() (string, []byte, error) {
	return "XOAUTH2", []byte("user=" + x.user + "\x01auth=Bearer " + x.token + "\x01\x01"), nil
}
func (x xoauth) Next([]byte) ([]byte, error) {
	return nil, errors.New("Outlook rejected OAuth authentication")
}
func (r *Reader) connect(ctx context.Context, folders ...string) (*client.Client, *imap.MailboxStatus, func(), error) {
	return connectWithRefresh(ctx, r.refreshAccess, func(token string) (*client.Client, *imap.MailboxStatus, func(), error) {
		return r.openMailbox(ctx, token, folders...)
	})
}

var errIMAPAuth = errors.New("Outlook IMAP OAuth login rejected")

// IMAP may reject a cached access token before its advertised expiry. Refresh
// once using the latest durable refresh token; no EVA login/OTP is submitted here.
func connectWithRefresh(ctx context.Context, token func(context.Context, bool) (string, error), open func(string) (*client.Client, *imap.MailboxStatus, func(), error)) (*client.Client, *imap.MailboxStatus, func(), error) {
	for attempt := 0; attempt < 2; attempt++ {
		access, err := token(ctx, attempt > 0)
		if err != nil {
			return nil, nil, nil, err
		}
		c, box, cleanup, err := open(access)
		if !errors.Is(err, errIMAPAuth) || attempt == 1 || ctx.Err() != nil {
			return c, box, cleanup, err
		}
		if cleanup != nil {
			cleanup()
		}
	}
	return nil, nil, nil, errIMAPAuth
}

func (r *Reader) openMailbox(ctx context.Context, token string, folders ...string) (*client.Client, *imap.MailboxStatus, func(), error) {
	dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 20 * time.Second}, Config: &tls.Config{ServerName: "outlook.office365.com", MinVersion: tls.VersionTLS12}}
	conn, err := dialer.DialContext(ctx, "tcp", "outlook.office365.com:993")
	if err != nil {
		return nil, nil, nil, errors.New("Outlook IMAP connection failed")
	}
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	cleanup := func() { stop(); conn.Close() }
	c, err := client.New(conn)
	if err != nil {
		cleanup()
		return nil, nil, nil, errors.New("Outlook IMAP greeting failed")
	}
	c.ErrorLog = log.New(io.Discard, "", 0)
	if err = c.Authenticate(xoauth{r.config.Email, token}); err != nil {
		cleanup()
		return nil, nil, nil, errIMAPAuth
	}
	folder := "INBOX"
	if len(folders) > 0 && folders[0] != "" {
		folder = folders[0]
	}
	box, err := c.Select(folder, true)
	if err != nil {
		cleanup()
		return nil, nil, nil, errors.New("Outlook inbox could not be opened read-only")
	}
	return c, box, cleanup, nil
}
func (r *Reader) Checkpoint(ctx context.Context) (Checkpoint, error) {
	_, box, close, err := r.connect(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	defer close()
	return Checkpoint{UIDValidity: box.UidValidity, NextUID: box.UidNext, Since: time.Now().UTC()}, nil
}

// Wait accepts only new inbox UIDs, matching recipient/sender/subject, with
// a code younger than EVA's three-minute validity. Captured emails are never used.
func (r *Reader) Wait(ctx context.Context, point Checkpoint) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 170*time.Second)
	defer cancel()
	for {
		code, err := r.poll(ctx, point)
		if err != nil {
			return "", err
		}
		if code != "" {
			return code, nil
		}
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", errors.New("no fresh EVA OTP arrived before the deadline")
		case <-timer.C:
		}
	}
}
func (r *Reader) poll(ctx context.Context, point Checkpoint) (string, error) {
	return r.pollMessage(ctx, point, subject, 3*time.Minute, Extract)
}
func (r *Reader) pollMessage(ctx context.Context, point Checkpoint, expectedSubject string, maxAge time.Duration, extract func([]byte, string, time.Time, time.Time) (string, error)) (string, error) {
	c, box, close, err := r.connect(ctx, point.Folder)
	if err != nil {
		return "", err
	}
	defer close()
	if box.UidValidity != point.UIDValidity {
		return "", errors.New("Outlook mailbox identity changed during login")
	}
	criteria := imap.NewSearchCriteria()
	criteria.Since = point.Since.Add(-24 * time.Hour)
	criteria.Uid = new(imap.SeqSet)
	criteria.Uid.AddRange(point.NextUID, 0)
	criteria.Header.Set("From", sender)
	criteria.Header.Set("Subject", expectedSubject)
	ids, err := c.UidSearch(criteria)
	if err != nil {
		return "", errors.New("Outlook OTP search failed")
	}
	var fresh []uint32
	for _, id := range ids {
		if id >= point.NextUID {
			fresh = append(fresh, id)
		}
	}
	if len(fresh) == 0 {
		return "", nil
	}
	if len(fresh) > 20 {
		fresh = fresh[len(fresh)-20:]
	}
	set := new(imap.SeqSet)
	set.AddNum(fresh...)
	section := &imap.BodySectionName{Peek: true, Partial: []int{0, 256*1024 + 1}}
	messages := make(chan *imap.Message, 20)
	if err = c.UidFetch(set, []imap.FetchItem{imap.FetchUid, imap.FetchInternalDate, section.FetchItem()}, messages); err != nil {
		return "", errors.New("Outlook OTP fetch failed")
	}
	var newest uint32
	var code string
	for message := range messages {
		body := message.GetBody(section)
		if body == nil {
			continue
		}
		if message.InternalDate.Before(point.Since.Add(-5*time.Second)) || time.Since(message.InternalDate) > maxAge {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(body, 256*1024+1))
		if err != nil || len(data) > 256*1024 {
			continue
		}
		value, err := extract(data, r.config.Email, point.Since, time.Now().UTC())
		if err == nil && message.Uid > newest {
			newest = message.Uid
			code = value
		}
	}
	return code, nil
}

var codePattern = regexp.MustCompile(`(?i)one-time password\s*\(OTP\)\s*is\s*:\s*([0-9]{6})\b`)
var tags = regexp.MustCompile(`<[^>]*>`)

func extractMessageBody(raw []byte, recipient string, since, now time.Time, expectedSubject string, maxAge time.Duration) (string, error) {
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", errors.New("invalid OTP email")
	}
	from, err := mail.ParseAddress(message.Header.Get("From"))
	if err != nil || !strings.EqualFold(from.Address, sender) {
		return "", errors.New("unrecognized OTP sender")
	}
	decoded, err := (&mime.WordDecoder{}).DecodeHeader(message.Header.Get("Subject"))
	if err != nil || decoded != expectedSubject {
		return "", errors.New("unrecognized OTP subject")
	}
	to, err := message.Header.AddressList("To")
	if err != nil {
		return "", errors.New("missing OTP recipient")
	}
	matches := false
	for _, a := range to {
		matches = matches || strings.EqualFold(a.Address, recipient)
	}
	if !matches {
		return "", errors.New("OTP recipient mismatch")
	}
	date, err := message.Header.Date()
	if err != nil || date.Before(since.Add(-5*time.Second)) || now.Sub(date) > maxAge || date.After(now.Add(30*time.Second)) {
		return "", errors.New("OTP email is stale")
	}
	body, err := decodeBody(message.Body, message.Header.Get("Content-Type"), message.Header.Get("Content-Transfer-Encoding"), 0)
	if err != nil {
		return "", err
	}
	return body, nil
}
func Extract(raw []byte, recipient string, since, now time.Time) (string, error) {
	body, err := extractMessageBody(raw, recipient, since, now, subject, 3*time.Minute)
	if err != nil {
		return "", err
	}
	text := html.UnescapeString(tags.ReplaceAllString(body, " "))
	found := codePattern.FindAllStringSubmatch(text, -1)
	if len(found) == 0 {
		return "", errors.New("OTP missing from EVA email")
	}
	code := found[0][1]
	for _, m := range found {
		if m[1] != code {
			return "", errors.New("ambiguous OTP email")
		}
	}
	return code, nil
}
func decodeBody(r io.Reader, kind, encoding string, depth int) (string, error) {
	if depth > 4 {
		return "", errors.New("OTP email nesting too deep")
	}
	switch strings.ToLower(encoding) {
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, r)
	}
	media, params, err := mime.ParseMediaType(kind)
	if err != nil {
		return "", errors.New("invalid OTP MIME type")
	}
	if strings.HasPrefix(media, "multipart/") {
		m := multipart.NewReader(r, params["boundary"])
		var out strings.Builder
		for i := 0; i < 20; i++ {
			p, e := m.NextRawPart()
			if e == io.EOF {
				return out.String(), nil
			}
			if e != nil {
				return "", errors.New("invalid OTP MIME body")
			}
			text, e := decodeBody(p, p.Header.Get("Content-Type"), p.Header.Get("Content-Transfer-Encoding"), depth+1)
			p.Close()
			if e != nil {
				return "", e
			}
			out.WriteString(text)
			if out.Len() > 256*1024 {
				return "", errors.New("OTP email too large")
			}
		}
		return "", errors.New("too many OTP MIME parts")
	}
	if media != "text/plain" && media != "text/html" {
		return "", nil
	}
	data, err := io.ReadAll(io.LimitReader(r, 256*1024+1))
	if err != nil || len(data) > 256*1024 {
		return "", fmt.Errorf("OTP email body unavailable")
	}
	return string(data), nil
}
