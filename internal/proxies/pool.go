// Package proxies leases one endpoint for an entire login, never per request.
package proxies

import (
	"awarddesk/internal/session"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

var ErrRemoved = errors.New("saved proxy slot was removed")

var sessionTag = regexp.MustCompile(`_(?:session|hardsession|lockedsession)-[^_]+`)
var lifetimeTag = regexp.MustCompile(`_lifetime-\d+`)

type entry struct {
	url   string
	busy  bool
	until time.Time
}
type Pool struct {
	mu          sync.Mutex
	path        string
	source      func() (string, error)
	sourceValue string
	entries     []*entry
	next        int
	mod         time.Time
	size        int64
}
type Lease struct {
	URL      string
	Template string
	release  func(bool)
	once     sync.Once
}

// Release quarantines transport failures briefly. It is safe to call twice.
func (l *Lease) Release(failed bool) { l.once.Do(func() { l.release(failed) }) }

func New(path string) (*Pool, error) {
	p := &Pool{path: path}
	if err := p.reload(); err != nil {
		return nil, err
	}
	return p, nil
}
func (p *Pool) Count() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.entries) }

// reload runs under the pool lock after initialization. Live leases survive
// edits/removals; invalid replacement files fail closed without leaking lines.
func (p *Pool) reload() error {
	if p.source != nil {
		value, err := p.source()
		if err != nil {
			return err
		}
		if value == p.sourceValue && len(p.entries) > 0 {
			return nil
		}
		if err = p.apply([]byte(value)); err != nil {
			return err
		}
		p.sourceValue = value
		return nil
	}
	info, err := os.Stat(p.path)
	if err != nil {
		return errors.New("proxy list could not be opened")
	}
	if info.Size() > 2*1024*1024 {
		return errors.New("proxy list exceeds 2 MiB")
	}
	if info.ModTime().Equal(p.mod) && info.Size() == p.size {
		return nil
	}
	data, err := os.ReadFile(p.path)
	if err != nil {
		return errors.New("proxy list could not be read")
	}
	if err = p.apply(data); err != nil {
		return err
	}
	p.mod, p.size = info.ModTime(), info.Size()
	return nil
}
func NewSource(source func() (string, error)) *Pool { return &Pool{source: source} }
func Validate(value string) error                   { return (&Pool{}).apply([]byte(value)) }
func (p *Pool) apply(data []byte) error {
	old := map[string]*entry{}
	for _, e := range p.entries {
		old[e.url] = e
	}
	seen := map[string]bool{}
	var next []*entry
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		value, err := session.ParseProxy(line)
		if err != nil {
			return fmt.Errorf("invalid proxy on line %d", i+1)
		}
		u, _ := url.Parse(value)
		// This integration requires Evomi's strict no-IP-replacement mode.
		if !strings.HasSuffix(u.Hostname(), ".evomi.com") || u.User == nil {
			return fmt.Errorf("line %d must be an authenticated Evomi endpoint", i+1)
		}
		pass, _ := u.User.Password()
		if len(sessionTag.FindAllString(pass, -1)) != 1 {
			return fmt.Errorf("line %d must contain exactly one sticky session tag", i+1)
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		e := old[value]
		if e == nil {
			e = &entry{url: value}
		}
		next = append(next, e)
	}
	if len(next) == 0 {
		return errors.New("proxy list contains no endpoints")
	}
	p.entries = next
	return nil
}

func lockedURL(raw string) (string, error) {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return "", errors.New("could not create sticky session identifier")
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return "", errors.New("invalid sticky proxy")
	}
	pass, _ := u.User.Password()
	pass = sessionTag.ReplaceAllString(pass, "_lockedsession-"+hex.EncodeToString(b))
	pass = lifetimeTag.ReplaceAllString(pass, "") // lifetime does not apply to locked sessions.
	u.User = url.UserPassword(u.User.Username(), pass)
	return u.String(), nil
}

// Acquire is bounded and nonblocking; no goroutine queue accumulates here.
// Each new lease gets a fresh 10-character ID. All requests use its exact URL.
func (p *Pool) Acquire(ctx context.Context) (*Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.reload(); err != nil {
		return nil, err
	}
	for offset := 0; offset < len(p.entries); offset++ {
		i := (p.next + offset) % len(p.entries)
		e := p.entries[i]
		if e.busy || time.Now().Before(e.until) {
			continue
		}
		value, err := lockedURL(e.url)
		if err != nil {
			return nil, err
		}
		e.busy = true
		p.next = (i + 1) % len(p.entries)
		return &Lease{URL: value, Template: e.url, release: func(failed bool) {
			p.mu.Lock()
			defer p.mu.Unlock()
			e.busy = false
			if failed {
				e.until = time.Now().Add(time.Minute)
			}
		}}, nil
	}
	return nil, errors.New("all proxy slots are busy or cooling down")
}

// Resume reserves the original pool slot without generating another session ID.
// A removed/changed endpoint cannot be used to restore an old airline session.
func (p *Pool) Resume(ctx context.Context, template, raw string) (*Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.reload(); err != nil {
		return nil, err
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return nil, errors.New("invalid saved proxy")
	}
	pass, _ := u.User.Password()
	tags := sessionTag.FindAllString(pass, -1)
	if len(tags) != 1 || !strings.HasPrefix(tags[0], "_lockedsession-") {
		return nil, errors.New("saved proxy is not locked")
	}
	normalize := func(value string) string {
		parsed, err := url.Parse(value)
		if err != nil || parsed.User == nil {
			return ""
		}
		password, _ := parsed.User.Password()
		password = sessionTag.ReplaceAllString(password, "_session-ID")
		password = lifetimeTag.ReplaceAllString(password, "")
		parsed.User = url.UserPassword(parsed.User.Username(), password)
		return parsed.String()
	}
	if normalize(raw) != normalize(template) {
		return nil, errors.New("saved proxy does not match pool slot")
	}
	for i, e := range p.entries {
		if e.url != template {
			continue
		}
		if e.busy || time.Now().Before(e.until) {
			return nil, errors.New("saved proxy slot unavailable")
		}
		e.busy = true
		p.next = (i + 1) % len(p.entries)
		return &Lease{URL: raw, Template: template, release: func(failed bool) {
			p.mu.Lock()
			defer p.mu.Unlock()
			e.busy = false
			if failed {
				e.until = time.Now().Add(time.Minute)
			}
		}}, nil
	}
	return nil, ErrRemoved
}
