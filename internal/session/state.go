package session

import (
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

// CookieEvent retains original scope and absolute expiry. Cookie headers alone
// omit these attributes and cannot safely reconstruct a jar.
type CookieEvent struct {
	URL     string
	Cookies []*http.Cookie
	At      time.Time `json:",omitempty"`
}
type State struct {
	UserAgent string
	EgressIP  string
	Cookies   []CookieEvent
}
type recordingJar struct {
	http.CookieJar
	mu       sync.Mutex
	events   []CookieEvent
	overflow bool
}

func (j *recordingJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.CookieJar.SetCookies(u, cookies)
	event := CookieEvent{URL: u.String(), At: time.Now()}
	for _, c := range cookies {
		copy := *c
		if copy.MaxAge > 0 {
			copy.Expires = time.Now().Add(time.Duration(copy.MaxAge) * time.Second)
			copy.MaxAge = 0
		}
		event.Cookies = append(event.Cookies, &copy)
	}
	if len(event.Cookies) == 0 {
		return
	}
	if len(j.events) >= 4096 {
		j.events = compactCookieEvents(j.events, time.Now())
		if len(j.events) >= 4096 {
			j.overflow = true
			return
		}
	}
	j.events = append(j.events, event)
}
func (c *Client) Snapshot() (State, error) {
	if c.jar == nil {
		return State{}, errors.New("session jar cannot be persisted")
	}
	c.jar.mu.Lock()
	defer c.jar.mu.Unlock()
	if c.jar.overflow {
		return State{}, errors.New("session cookie history exceeded limit")
	}
	return State{UserAgent: c.userAgent, EgressIP: c.egressIP, Cookies: compactCookieEvents(c.jar.events, time.Now())}, nil
}

// Retain each live cookie's latest attributes in creation order. Updates do
// not change cookie creation order, while deletion/recreation does. Scope and
// absolute expiry remain intact, including cookies with a default path.
func compactCookieEvents(events []CookieEvent, now time.Time) []CookieEvent {
	type stored struct {
		event CookieEvent
		order int
	}
	live := map[string]stored{}
	sequence := 0
	for _, event := range events {
		u, err := url.Parse(event.URL)
		if err != nil {
			continue
		}
		host := strings.ToLower(u.Hostname())
		for _, cookie := range event.Cookies {
			if cookie == nil || cookie.Name == "" {
				continue
			}
			domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
			if cookie.Domain == "" {
				domain = host
			} else if domain == "" || (domain != host && !strings.HasSuffix(host, "."+domain)) {
				continue
			}
			path := cookie.Path
			if !strings.HasPrefix(path, "/") {
				path = "/"
				if index := strings.LastIndex(u.Path, "/"); index > 0 {
					path = u.Path[:index]
				}
			}
			key := domain + "\x00" + path + "\x00" + cookie.Name
			old, exists := live[key]
			if exists && !event.At.IsZero() && !old.event.Cookies[0].Expires.IsZero() && !old.event.Cookies[0].Expires.After(event.At) {
				delete(live, key)
				exists = false
			}
			if cookie.MaxAge < 0 || (!event.At.IsZero() && !cookie.Expires.IsZero() && !cookie.Expires.After(event.At)) {
				delete(live, key)
				continue
			}
			order := sequence
			if exists {
				order = old.order
			} else {
				sequence++
			}
			copy := *cookie
			live[key] = stored{event: CookieEvent{URL: event.URL, Cookies: []*http.Cookie{&copy}, At: event.At}, order: order}
		}
	}
	ordered := make([]stored, 0, len(live))
	for _, item := range live {
		if expiry := item.event.Cookies[0].Expires; !expiry.IsZero() && !expiry.After(now) {
			continue
		}
		ordered = append(ordered, item)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].order < ordered[j].order })
	result := make([]CookieEvent, 0, len(ordered))
	for _, item := range ordered {
		result = append(result, item.event)
	}
	return result
}

// Restore is only permitted before the first request. Replay preserves cookie
// updates/deletions, host-only domains, paths and expiry across process restarts.
func (c *Client) Restore(s State) error {
	if c.count != 0 || len(s.Cookies) > 4096 {
		return errors.New("invalid saved session")
	}
	if err := c.SetUserAgent(s.UserAgent); err != nil {
		return err
	}
	if s.EgressIP != "" {
		if err := c.PinEgressIP(s.EgressIP); err != nil {
			return err
		}
	}
	for _, event := range s.Cookies {
		u, err := url.Parse(event.URL)
		if err != nil || u.Scheme != "https" || u.User != nil || !c.hosts[u.Host] {
			return errors.New("invalid saved cookie origin")
		}
		for _, cookie := range event.Cookies {
			if cookie == nil {
				return errors.New("invalid saved cookie")
			}
		}
		c.SetCookies(u, event.Cookies)
	}
	return nil
}
