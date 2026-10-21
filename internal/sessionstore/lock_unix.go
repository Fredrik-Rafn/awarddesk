//go:build darwin || linux

package sessionstore

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// The OS releases flock ownership on process exit; a leftover .lock file is
// harmless. Each account has its own lock, allowing independent sessions.
func (s Store) Lock() (func(), error) {
	if s.Backend != nil {
		return s.Backend.LockState(s.Path)
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return nil, errors.New("cannot create session lock directory")
	}
	f, err := os.OpenFile(s.Path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("cannot open session lock")
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("saved session is in use by another process")
	}
	var once sync.Once
	return func() { once.Do(func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }) }, nil
}
