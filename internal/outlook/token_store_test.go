package outlook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"awarddesk/internal/sessionstore"
)

func TestPortableRotatedTokenAndAccountIsolation(t *testing.T) {
	store := sessionstore.Store{Path: filepath.Join(t.TempDir(), "mailbox.json")}
	r := New(Config{Email: "test@example.test", ClientID: "test-client", RefreshToken: "configured-token"})
	if token, err := r.loadRefreshToken(store); err != nil || token != "configured-token" {
		t.Fatal("missing file did not use configuration", err)
	}
	if err := r.saveRefreshToken(store, "rotated-token"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.Path)
	if err != nil || !strings.Contains(string(data), `"refresh_token": "rotated-token"`) {
		t.Fatal("token is not readable JSON", err)
	}
	if token, err := r.loadRefreshToken(store); err != nil || token != "rotated-token" {
		t.Fatal("rotated token was not reused", err)
	}
	for _, other := range []Config{{Email: "other@example.test", ClientID: "test-client"}, {Email: r.config.Email, ClientID: "other-client"}} {
		if _, err := New(other).loadRefreshToken(store); err == nil {
			t.Fatal("another account's token was accepted")
		}
	}
	if r.tokenStore().Path == New(Config{Email: "other@example.test", ClientID: r.config.ClientID}).tokenStore().Path {
		t.Fatal("account paths collide")
	}
}
