package registration

import (
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
)

type Mailbox struct{ Email, ClientID, RefreshToken string }

var uuid = regexp.MustCompile(`^[a-fA-F0-9]{8}(?:-[a-fA-F0-9]{4}){3}-[a-fA-F0-9]{12}$`)

// Passwords in the colon format are accepted but not retained or used: Outlook
// access uses OAuth2 exclusively. Never include raw input in validation errors.
func ParseMailboxes(text string) ([]Mailbox, error) {
	var result []Mailbox
	seen := map[string]bool{}
	for i, line := range strings.Split(strings.TrimPrefix(text, "\ufeff"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 4 {
			return nil, fmt.Errorf("Line %d: expected email:password:refresh_token:client_id", i+1)
		}
		email := strings.ToLower(strings.TrimSpace(parts[0]))
		parsed, err := mail.ParseAddress(email)
		clientID := strings.TrimSpace(parts[len(parts)-1])
		refreshToken := strings.TrimSpace(parts[len(parts)-2])
		if err != nil || parsed.Address != email || !uuid.MatchString(clientID) || len(refreshToken) < 20 || len(refreshToken) > 16384 {
			return nil, fmt.Errorf("Line %d: invalid email, OAuth client ID or refresh token", i+1)
		}
		if seen[email] {
			continue
		}
		seen[email] = true
		result = append(result, Mailbox{email, clientID, refreshToken})
		if len(result) > 500 {
			return nil, errors.New("Import at most 500 mailboxes per batch")
		}
	}
	if len(result) == 0 {
		return nil, errors.New("Enter at least one Outlook credential line")
	}
	return result, nil
}
