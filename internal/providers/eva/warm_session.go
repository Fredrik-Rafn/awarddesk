package eva

import (
	"awarddesk/internal/session"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"time"
)

const memberVerificationTTL = 2 * time.Minute
const warmSessionIdleTTL = 5 * time.Minute

type warmSession struct {
	client             *session.Client
	diskKey            [32]byte
	proxy              string
	verifiedAt, usedAt time.Time
}

func sessionSnapshotKey(data []byte) [32]byte {
	var compact bytes.Buffer
	if json.Compact(&compact, data) == nil {
		return sha256.Sum256(compact.Bytes())
	}
	return sha256.Sum256(data)
}

// JSON on disk remains authoritative, including changes by another process.
// Never reuse transport or verification across a changed snapshot or proxy.
func (p *Provider) sessionClient(proxy string, data []byte, saved bool) (*session.Client, bool, error) {
	now := time.Now()
	key := sessionSnapshotKey(data)
	if w := p.warm; saved && w != nil && w.proxy == proxy && w.diskKey == key && !w.usedAt.After(now) && now.Sub(w.usedAt) < warmSessionIdleTTL {
		w.client.BeginOperation()
		w.usedAt = now
		return w.client, true, nil
	}
	p.discardWarm()
	c, err := session.NewWithTimeout(proxy, []string{"booking.evaair.com", "eservice.evaair.com", "book.evaair.com", "api.ipify.org"}, 90*time.Second)
	if err != nil {
		return nil, false, err
	}
	p.warm = &warmSession{client: c, diskKey: key, proxy: proxy, usedAt: now}
	return c, false, nil
}

func (p *Provider) recentlyVerified(now time.Time) bool {
	return p.warm != nil && !p.warm.verifiedAt.IsZero() && !p.warm.verifiedAt.After(now) && now.Sub(p.warm.verifiedAt) < memberVerificationTTL
}
func (p *Provider) markVerified(now time.Time) {
	if p.warm != nil {
		p.warm.verifiedAt = now
	}
}
func (p *Provider) discardWarm() {
	if p.warm != nil {
		p.warm.client.Close()
		p.warm = nil
	}
}

func (p *Provider) Close() {
	p.gate <- struct{}{}
	defer func() { <-p.gate }()
	p.closed = true
	p.discardWarm()
}
