package ana

import (
	"awarddesk/internal/award"
	"context"
	"errors"
	"os"
	"time"
)

type sessionStore interface {
	Load() ([]byte, error)
	Save([]byte) error
	Delete() error
}

// Manager owns the load/validate/login/save lifecycle. Future search code must
// use WithSession; it must not close or retain the session outside its callback.
// No callback (including a search) is replayed automatically.
type Manager struct {
	store   sessionStore
	gate    chan struct{}
	login   func(context.Context, award.Reporter) (*LoginSession, error)
	restore func(context.Context, []byte) (*LoginSession, error)
	verify  func(context.Context, *LoginSession, award.Reporter) error
	save    func(*LoginSession) ([]byte, error)
	close   func(*LoginSession)
}

func NewManager(p *Provider, store sessionStore) *Manager {
	return &Manager{store: store, gate: make(chan struct{}, 1), login: p.Login, restore: p.restore, verify: p.verify, save: p.save, close: func(s *LoginSession) { s.Close() }}
}
func (m *Manager) Ensure(ctx context.Context, report award.Reporter) error {
	return m.WithSession(ctx, report, nil)
}
func (m *Manager) WithSession(ctx context.Context, report award.Reporter, use func(context.Context, *LoginSession) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case m.gate <- struct{}{}:
		defer func() { <-m.gate }()
	default:
		return award.Failure("busy", "ANA session is already in use")
	}
	if locker, ok := m.store.(interface{ Lock() (func(), error) }); ok {
		unlock, err := locker.Lock()
		if err != nil {
			return award.Failure("busy", "ANA saved session is locked or unavailable")
		}
		defer unlock()
	}
	ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	var current *LoginSession
	data, err := m.store.Load()
	if err == nil {
		current, err = m.restore(ctx, data)
		if err == nil {
			err = m.verify(ctx, current, report)
		}
		if err != nil {
			if current != nil {
				m.close(current)
				current = nil
			}
			if !errors.Is(err, errExpired) {
				return err
			}
			if err = m.store.Delete(); err != nil {
				return award.Failure("session_store", "Could not remove expired ANA session")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return award.Failure("session_store", "Saved ANA session could not be read; no credentials were submitted")
	}
	if current == nil {
		if report != nil {
			report(award.Progress{Stage: "session login", Message: "No valid saved ANA session; logging in"})
		}
		current, err = m.login(ctx, report)
		if err != nil {
			return err
		}
	}
	defer m.close(current)
	// Persist only a member-verified session, including cookies refreshed by
	// validation. Failed login/OTP/403 responses never overwrite a good file.
	if err = m.persist(current); err != nil {
		return err
	}
	if use != nil {
		if err = use(ctx, current); err != nil {
			return err
		}
		if err = m.persist(current); err != nil {
			return err
		}
	}
	if report != nil {
		report(award.Progress{Stage: "session ready", Message: "ANA session verified and saved for reuse"})
	}
	return nil
}
func (m *Manager) persist(s *LoginSession) error {
	data, err := m.save(s)
	if err == nil {
		err = m.store.Save(data)
	}
	if err != nil {
		return award.Failure("session_store", "ANA session verified but could not be saved")
	}
	return nil
}
