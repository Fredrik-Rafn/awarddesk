package proxies

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const fixture = "http://core-residential.evomi.com:1000:user:private-secret_session-abcdefghij_lifetime-3"

func TestWaitResumePreservesIdentityAndHonorsCancellation(t *testing.T) {
	p, _ := poolFile(t, fixture)
	a, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = p.WaitResume(ctx, a.Template, a.URL); err != context.DeadlineExceeded {
		t.Fatal("busy proxy did not honor cancellation")
	}
	original := a.URL
	a.Release(true)
	p.mu.Lock()
	p.sticky[original].until = time.Now().Add(20 * time.Millisecond)
	p.mu.Unlock()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	b, err := p.WaitResume(ctx2, a.Template, original)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Release(false)
	if b.URL != original {
		t.Fatal("waiting changed saved session identity")
	}
}

func poolFile(t *testing.T, lines string) (*Pool, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "proxies.txt")
	if err := os.WriteFile(path, []byte(lines), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	return p, path
}
func TestExclusiveFreshLockedLeases(t *testing.T) {
	p, _ := poolFile(t, fixture+"\n"+fixture)
	if p.Count() != 1 {
		t.Fatal("duplicates were not removed")
	}
	a, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.URL, "_lockedsession-") || strings.Contains(a.URL, "_lifetime-") || strings.Contains(a.URL, "_session-") {
		t.Fatal("proxy was not locked")
	}
	if _, err := p.Acquire(context.Background()); err == nil {
		t.Fatal("slot leased twice")
	}
	first := a.URL
	a.Release(false)
	a.Release(true) // second release must not quarantine the slot
	b, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b.URL == first {
		t.Fatal("fresh lease reused the previous session ID")
	}
	b.Release(true)
	if _, err := p.Acquire(context.Background()); err == nil {
		t.Fatal("quarantined endpoint was leased")
	}
}

func TestResumeKeepsIdentityAndFreshLoginUsesNextSlot(t *testing.T) {
	p, path := poolFile(t, fixture+"\n"+strings.Replace(fixture, "abcdefghij", "0123456789", 1))
	first, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, template := first.URL, first.Template
	first.Release(false)
	restarted, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := restarted.Resume(context.Background(), template, raw)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.URL != raw {
		t.Fatal("saved session proxy identity changed")
	}
	if other, err := restarted.Resume(context.Background(), template, raw); err == nil {
		other.Release(false)
		t.Fatal("saved slot leased twice")
	}
	resumed.Release(false)
	next, err := restarted.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Release(false)
	if next.Template == template || next.URL == raw {
		t.Fatal("fresh login did not advance to next unused slot")
	}
	if _, err := restarted.Resume(context.Background(), template, strings.Replace(raw, "core-residential.evomi.com", "untrusted.test", 1)); err == nil {
		t.Fatal("saved proxy destination not tied to pool")
	}
}
func TestProxyReloadAndCancellation(t *testing.T) {
	p, path := poolFile(t, fixture)
	a, _ := p.Acquire(context.Background())
	if err := os.WriteFile(path, []byte(fixture+"\n"+strings.Replace(fixture, "abcdefghij", "0123456789", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := p.Acquire(context.Background())
	if err != nil || p.Count() != 2 {
		t.Fatal("new proxy was not loaded")
	}
	defer b.Release(false)
	a.Release(false)
	if err := os.WriteFile(path, []byte("bad private-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Acquire(context.Background()); err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatal("invalid replacement accepted or leaked")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Acquire(ctx); err != context.Canceled {
		t.Fatal("cancellation ignored")
	}
}
func TestConcurrentLeaseBound(t *testing.T) {
	p, _ := poolFile(t, fixture)
	var wg sync.WaitGroup
	var mu sync.Mutex
	count := 0
	start := make(chan struct{})
	release := make(chan struct{})
	attempted := make(chan struct{}, 200)
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			l, err := p.Acquire(context.Background())
			attempted <- struct{}{}
			if err == nil {
				mu.Lock()
				count++
				mu.Unlock()
				<-release
				l.Release(false)
			}
		}()
	}
	close(start)
	// Hold the single successful lease while all other requests are rejected.
	for i := 0; i < 200; i++ {
		<-attempted
	}
	close(release)
	wg.Wait()
	if count != 1 {
		t.Fatalf("expected one live lease, got %d", count)
	}
}
