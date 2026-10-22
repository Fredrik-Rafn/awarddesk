package proxies

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIndependentSavedIdentitiesShareTemplateWithoutBlocking(t *testing.T) {
	p, _ := poolFile(t, fixture)
	a, _ := p.Acquire(context.Background())
	a.Release(false)
	b, _ := p.Acquire(context.Background())
	b.Release(false)
	a, err := p.Resume(context.Background(), a.Template, a.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, err = p.Resume(context.Background(), b.Template, b.URL)
	if err != nil {
		a.Release(false)
		t.Fatal("independent sticky sessions serialized", err)
	}
	if _, err = p.Resume(context.Background(), a.Template, a.URL); !errors.Is(err, ErrUnavailable) {
		t.Fatal("same identity overlapped")
	}
	a.Release(true)
	if _, err = p.Resume(context.Background(), a.Template, a.URL); !errors.Is(err, ErrUnavailable) {
		t.Fatal("failed identity was not quarantined")
	}
	b.Release(false)
	b, err = p.Resume(context.Background(), b.Template, b.URL)
	if err != nil {
		t.Fatal("another identity inherited the failed IP cooldown", err)
	}
	b.Release(false)
}

func TestResumeWaiterWakesOnRelease(t *testing.T) {
	p, _ := poolFile(t, fixture)
	a, _ := p.Acquire(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		lease, err := p.WaitResume(ctx, a.Template, a.URL)
		if err == nil {
			lease.Release(false)
		}
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	a.Release(false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("waiter waited for polling rather than the release notification")
	}
}

func TestSourceCacheInvalidatesImmediatelyAfterProxyEdit(t *testing.T) {
	value, calls := fixture, 0
	p := NewSource(func() (string, error) { calls++; return value, nil })
	a, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.Release(false)
	b, _ := p.Acquire(context.Background())
	b.Release(false)
	if calls != 1 {
		t.Fatal("source reread unnecessarily")
	}
	value = "invalid secret-value"
	p.Invalidate()
	if _, err = p.Acquire(context.Background()); err == nil {
		t.Fatal("edited invalid list was not rejected immediately")
	}
}
