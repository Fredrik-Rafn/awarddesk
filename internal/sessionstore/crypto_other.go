//go:build !windows

package sessionstore

import "errors"

func crypt([]byte, bool) ([]byte, error) {
	return nil, errors.New("encrypted session persistence requires Windows")
}
