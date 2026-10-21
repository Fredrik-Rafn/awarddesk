package fleet

import (
	"awarddesk/internal/proxies"
	"context"
	"errors"
	"strconv"
	"sync"
	"time"
)

// Registration shares both the worker ceiling and proxy leases with searches
// and session preparation. Each acquisition creates a new locked sticky identity.
func (f *Fleet) RegistrationLease(ctx context.Context, saved ...string) (*proxies.Lease, func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		limit, _ := strconv.Atoi(f.db.Setting("SEARCH_CONCURRENCY"))
		if limit < 1 {
			limit = 4
		}
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			return nil, nil, errors.New("fleet is closed")
		}
		active := f.registrations
		for _, entry := range f.entries {
			if entry.busy {
				active++
			}
		}
		if active < limit {
			var lease *proxies.Lease
			var err error
			if len(saved) == 2 && saved[0] != "" {
				lease, err = f.pool.Resume(ctx, saved[0], saved[1])
			} else {
				lease, err = f.pool.Acquire(ctx)
			}
			if err == nil {
				f.registrations++
				f.mu.Unlock()
				var once sync.Once
				return lease, func() { once.Do(func() { lease.Release(false); f.mu.Lock(); f.registrations--; f.mu.Unlock() }) }, nil
			}
			if !errors.Is(err, proxies.ErrUnavailable) {
				f.mu.Unlock()
				return nil, nil, err
			}
		}
		f.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}
