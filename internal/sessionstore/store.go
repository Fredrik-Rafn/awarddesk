// Package sessionstore persists portable, readable JSON state files.
package sessionstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const maxSize = 8 * 1024 * 1024

type Store struct{ Path string }

func (s Store) Load() ([]byte, error) {
	info, err := os.Stat(s.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, errors.New("cannot read saved session")
	}
	if info.Size() == 0 || info.Size() > maxSize {
		return nil, errors.New("invalid saved session size")
	}
	f, err := os.Open(s.Path)
	if err != nil {
		return nil, errors.New("cannot read saved session")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil || len(data) == 0 || len(data) > maxSize {
		return nil, errors.New("invalid saved session contents")
	}
	if !json.Valid(data) {
		return nil, errors.New("saved state is not valid JSON")
	}
	return data, nil
}
func (s Store) Save(data []byte) error {
	if len(data) == 0 || len(data) > maxSize {
		return errors.New("invalid session size")
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, data, "", "  "); err != nil {
		return errors.New("saved state must be valid JSON")
	}
	formatted.WriteByte('\n')
	if formatted.Len() > maxSize {
		return errors.New("formatted state exceeds size limit")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return errors.New("cannot create session directory")
	}
	f, err := os.CreateTemp(filepath.Dir(s.Path), ".session-*")
	if err != nil {
		return errors.New("cannot create session file")
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	err = f.Chmod(0600)
	if err == nil {
		_, err = f.Write(formatted.Bytes())
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.New("cannot write session file")
	}
	if err = os.Rename(tmp, s.Path); err != nil {
		return errors.New("cannot replace session file")
	}
	return nil
}
func (s Store) Delete() error {
	err := os.Remove(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("cannot remove expired session")
	}
	return nil
}
