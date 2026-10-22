package eva

import (
	"testing"
	"time"

	"awarddesk/internal/sessionstore"
)

func TestWarmSessionReusesOnlyUnchangedDiskStateAndProxy(t *testing.T) {
	p := New(Config{})
	defer p.Close()
	c, reused, err := p.sessionClient("", []byte("snapshot"), true)
	if err != nil || reused {
		t.Fatal("unexpected initial reuse", err)
	}
	p.markVerified(time.Now())
	again, reused, err := p.sessionClient("", []byte("snapshot"), true)
	if err != nil || !reused || again != c || !p.recentlyVerified(time.Now()) {
		t.Fatal("unchanged session was not reused", err)
	}
	changed, reused, err := p.sessionClient("", []byte("external update"), true)
	if err != nil || reused || changed == c || p.recentlyVerified(time.Now()) {
		t.Fatal("external state retained stale transport or verification", err)
	}
	other, reused, err := p.sessionClient("http://127.0.0.1:9999", []byte("external update"), true)
	if err != nil || reused || other == changed {
		t.Fatal("transport crossed proxy", err)
	}
	p.warm.usedAt = time.Now().Add(-warmSessionIdleTTL)
	idle, reused, err := p.sessionClient("http://127.0.0.1:9999", []byte("external update"), true)
	if err != nil || reused || idle == other {
		t.Fatal("idle client survived expiry", err)
	}
}

func TestWarmSessionSaveUpdatesFingerprintAndVerificationDoesNotSlide(t *testing.T) {
	p := New(Config{})
	defer p.Close()
	c, _, err := p.sessionClient("", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	checked := time.Now()
	p.markVerified(checked)
	store := sessionstore.Store{Path: t.TempDir() + "/session.json"}
	if err = p.save(store, c, cached{Version: 1}); err != nil {
		t.Fatal(err)
	}
	data, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	again, reused, err := p.sessionClient("", data, true)
	if err != nil || !reused || again != c {
		t.Fatal("saved client lost ownership", err)
	}
	if !p.warm.verifiedAt.Equal(checked) {
		t.Fatal("reuse extended verification TTL")
	}
	for _, now := range []time.Time{checked.Add(memberVerificationTTL), checked.Add(-time.Second)} {
		if p.recentlyVerified(now) {
			t.Fatal("expired or future verification reused")
		}
	}
	p.discardWarm()
	if p.recentlyVerified(time.Now()) {
		t.Fatal("discarded verification remained valid")
	}
}
