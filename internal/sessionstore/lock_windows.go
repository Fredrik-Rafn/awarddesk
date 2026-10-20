//go:build windows

package sessionstore

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"sync"
)

// Lock is released by Windows even when the process crashes. A leftover file
// is harmless: ownership is an OS lock, not the existence of the file.
func (s Store) Lock() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return nil, errors.New("cannot create session lock directory")
	}
	f, err := os.OpenFile(s.Path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("cannot open session lock")
	}
	overlapped := &windows.Overlapped{}
	if err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped); err != nil {
		f.Close()
		return nil, errors.New("saved session is in use by another process")
	}
	var once sync.Once
	return func() {
		once.Do(func() { windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, overlapped); f.Close() })
	}, nil
}
