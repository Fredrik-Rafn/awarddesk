package fleet

import (
	"context"
	"errors"
	"sync"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/proxies"
)

func (f *Fleet) RegistrationLease(ctx context.Context, saved ...string) (*proxies.Lease, func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		limit := award.ConcurrencyLimit(f.db.Setting("SEARCH_CONCURRENCY"))
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
				return lease, func() {
					once.Do(func() { lease.Release(false); f.mu.Lock(); f.registrations--; f.notifyLocked(); f.mu.Unlock() })
				}, nil
			}
			if !errors.Is(err, proxies.ErrUnavailable) {
				f.mu.Unlock()
				return nil, nil, err
			}
		}
		changed := f.changed
		f.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-changed:
		case <-time.After(time.Second):
		}
	}
}
