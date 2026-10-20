//go:build !windows && !darwin && !linux

package sessionstore

import "errors"

func (Store) Lock() (func(), error) {
	return nil, errors.New("session locking is not supported on this operating system")
}
