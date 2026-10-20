//go:build !windows

package sessionstore

import "errors"

func (Store) Lock() (func(), error) { return nil, errors.New("session storage requires Windows") }
