package outlook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"awarddesk/internal/sessionstore"
)

type savedMailboxToken struct {
	Email        string    `json:"email"`
	ClientID     string    `json:"client_id"`
	RefreshToken string    `json:"refresh_token"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (r *Reader) tokenStore() sessionstore.Store {
	hash := sha256.Sum256([]byte(strings.ToLower(r.config.Email) + "|" + r.config.ClientID))
	return sessionstore.Store{Backend: r.config.StateBackend, Path: filepath.Join(".state", "outlook", hex.EncodeToString(hash[:])+".json")}
}

func (r *Reader) loadRefreshToken(store sessionstore.Store) (string, error) {
	data, err := store.Load()
	if errors.Is(err, os.ErrNotExist) {
		return r.config.RefreshToken, nil
	}
	if err != nil {
		return "", errors.New("Outlook token file could not be read")
	}
	var saved savedMailboxToken
	if json.Unmarshal(data, &saved) != nil || !strings.EqualFold(saved.Email, r.config.Email) || saved.ClientID != r.config.ClientID || saved.RefreshToken == "" {
		return "", errors.New("Outlook token file does not match the configured account")
	}
	return saved.RefreshToken, nil
}

func (r *Reader) saveRefreshToken(store sessionstore.Store, token string) error {
	data, err := json.Marshal(savedMailboxToken{Email: r.config.Email, ClientID: r.config.ClientID, RefreshToken: token, UpdatedAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	return store.Save(data)
}
