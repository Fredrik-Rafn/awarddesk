package outlook

import (
	"context"
	"errors"
	"testing"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

func TestMailboxAuthRefreshIsBoundedAndOnlyForAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failures []error
		calls    int
		want     error
	}{
		{"stale access recovers", []error{errIMAPAuth, nil}, 2, nil},
		{"persistent rejection stops", []error{errIMAPAuth, errIMAPAuth}, 2, errIMAPAuth},
		{"network failure does not rotate", []error{context.DeadlineExceeded}, 1, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issued, opened := 0, 0
			_, _, _, err := connectWithRefresh(context.Background(), func(_ context.Context, force bool) (string, error) {
				if force != (issued > 0) {
					t.Fatal("incorrect force refresh")
				}
				issued++
				return "opaque-test-token", nil
			}, func(string) (*client.Client, *imap.MailboxStatus, func(), error) {
				e := tc.failures[opened]
				opened++
				return nil, nil, nil, e
			})
			if issued != tc.calls || opened != tc.calls || !errors.Is(err, tc.want) {
				t.Fatal("retry contract violated")
			}
		})
	}
}
