package login

import (
	"testing"

	"awarddesk/internal/award"
)

func TestStatusOwnsItsRecoveryHistory(t *testing.T) {
	s := New(nil)
	defer s.Close()
	s.status.Recoveries = []award.Recovery{{Code: "connection"}}
	status := s.Status()
	status.Recoveries[0].Code = "modified"
	if s.Status().Recoveries[0].Code != "connection" {
		t.Fatal("caller mutated the service's recovery history")
	}
}
