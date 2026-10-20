package ana

import (
	"awarddesk/internal/award"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

type memoryStore struct {
	data             []byte
	saves, deletes   int
	loadErr, saveErr error
}

func (s *memoryStore) Load() ([]byte, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	if s.data == nil {
		return nil, os.ErrNotExist
	}
	return s.data, nil
}
func (s *memoryStore) Save(data []byte) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saves++
	s.data = data
	return nil
}
func (s *memoryStore) Delete() error { s.deletes++; s.data = nil; return nil }
func TestManagerLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name                string
		cached              bool
		verifyErr, loginErr error
		wantLogin, wantSave int
		wantError           bool
	}{
		{"missing", false, nil, nil, 1, 1, false},
		{"valid", true, nil, nil, 0, 1, false},
		{"expired", true, errExpired, nil, 1, 1, false},
		{"otp", false, nil, award.Failure("mfa_required", "OTP"), 1, 0, true},
		{"denied", true, award.Failure("access_denied", "denied"), nil, 0, 0, true},
		{"connection", true, award.Failure("connection", "uncertain"), nil, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &memoryStore{}
			if tc.cached {
				store.data = []byte("cached")
			}
			logins, closed := 0, 0
			m := &Manager{store: store, gate: make(chan struct{}, 1),
				login: func(context.Context, award.Reporter) (*LoginSession, error) {
					logins++
					if tc.loginErr != nil {
						return nil, tc.loginErr
					}
					return &LoginSession{}, nil
				},
				restore: func(context.Context, []byte) (*LoginSession, error) { return &LoginSession{}, nil },
				verify:  func(context.Context, *LoginSession, award.Reporter) error { return tc.verifyErr },
				save:    func(*LoginSession) ([]byte, error) { return []byte("saved"), nil },
				close:   func(*LoginSession) { closed++ },
			}
			err := m.Ensure(context.Background(), nil)
			if (err != nil) != tc.wantError || logins != tc.wantLogin || store.saves != tc.wantSave {
				t.Fatalf("err=%v logins=%d saves=%d", err, logins, store.saves)
			}
			if tc.cached && tc.wantError && string(store.data) != "cached" {
				t.Fatal("failed validation overwrote cache")
			}
			if tc.name == "expired" && (store.deletes != 1 || closed != 2) {
				t.Fatal("expired session was not closed before replacement")
			}
		})
	}
}
func TestManagerNoWorkOnCorruptionBusyOrCancelled(t *testing.T) {
	store := &memoryStore{loadErr: errors.New("corrupt")}
	m := &Manager{store: store, gate: make(chan struct{}, 1)}
	if err := m.Ensure(context.Background(), nil); err == nil {
		t.Fatal("corrupt cache ignored")
	}
	m.gate <- struct{}{}
	var ae *award.Error
	if err := m.Ensure(context.Background(), nil); !errors.As(err, &ae) || ae.Code != "busy" {
		t.Fatal("overlapping use accepted")
	}
	<-m.gate
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(m.Ensure(ctx, nil), context.Canceled) {
		t.Fatal("cancel ignored")
	}
}
func TestCacheExpiryAndAccountIsolation(t *testing.T) {
	p := New(Config{MemberNumber: "0000000000", Password: "synthetic"})
	base := savedSession{Version: 1, Account: p.AccountID(), Identity: p.cacheIdentity(), CreatedAt: time.Now(), Token: "token", ClientID: "client", ClientKey: "key"}
	for _, change := range []func(*savedSession){
		func(s *savedSession) { s.CreatedAt = time.Now().Add(-maxSessionAge - time.Second) },
		func(s *savedSession) { s.Account = "another-account" },
		func(s *savedSession) { s.Identity = "old-password-or-proxy" },
	} {
		s := base
		change(&s)
		data, _ := json.Marshal(s)
		if _, err := p.restore(context.Background(), data); !errors.Is(err, errExpired) {
			t.Fatal("stale identity accepted", err)
		}
	}
}
