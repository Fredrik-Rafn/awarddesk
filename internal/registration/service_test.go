package registration

import (
	"awarddesk/internal/award"
	"awarddesk/internal/fleet"
	"awarddesk/internal/providers/eva"
	"awarddesk/internal/storage"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const mailboxFixture = "test@outlook.com:discard-this-password:private-refresh-token-1234:9e5f94bc-e8a4-4e73-b8be-63364c29d753"

func testService(t *testing.T) *Service {
	t.Helper()
	db, e := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(db, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close(); db.Close() })
	return s
}
func TestBackgroundSessionRecoveryUpdatesOnboardingWithoutNewSignup(t *testing.T) {
	s := testService(t)
	s.Import(mailboxFixture)
	items, _ := s.loadAll()
	r := items[0]
	r.Status = "failed"
	r.State.Phase = "created"
	r.AccountID = "created-account"
	r.Error = "transient preparation failure"
	s.save(r)
	s.syncReady([]fleet.Status{{ID: r.AccountID, State: "ready", Session: eva.SessionInfo{Ready: true}}})
	rows, total, e := s.List(1, 25)
	if e != nil || total != 1 || rows[0].Status != "ready" || rows[0].Error != "" {
		t.Fatal("background recovery not reflected")
	}
	if e := s.Retry(r.ID); e == nil {
		t.Fatal("completed registration requeued")
	}
}
func TestPreparationRetryNeverReplaysUncertainCredentials(t *testing.T) {
	if !retrySessionPreparation(&award.Error{Code: "connection", Stage: "proxy verification"}) {
		t.Fatal("safe preparation not retried")
	}
	for _, failure := range []*award.Error{{Code: "connection", Stage: "credentials", OutcomeUnknown: true}, {Code: "connection", Stage: "otp submission", OutcomeUnknown: true}, {Code: "connection", Stage: "verification continuation", OutcomeUnknown: true}, {Code: "login_response", Stage: "credentials"}} {
		if retrySessionPreparation(failure) {
			t.Fatal("unsafe credential/OTP replay")
		}
	}
}
func TestBulkImportValidationDedupAndSecretBoundary(t *testing.T) {
	s := testService(t)
	if _, e := s.Import(mailboxFixture + "\ninvalid secret-input"); e == nil {
		t.Fatal("partial batch accepted")
	}
	if _, total, _ := s.List(1, 25); total != 0 {
		t.Fatal("invalid batch partially imported")
	}
	result, e := s.Import(mailboxFixture + "\n" + mailboxFixture)
	if e != nil || result.Added != 1 {
		t.Fatal(result, e)
	}
	result, e = s.Import(mailboxFixture)
	if e != nil || result.Added != 0 || result.Skipped != 1 {
		t.Fatal(result, e)
	}
	rows, total, e := s.List(1, 25)
	b, _ := json.Marshal(rows)
	if e != nil || total != 1 || strings.Contains(string(b), "private-refresh") || strings.Contains(string(b), "password") {
		t.Fatal("summary leaked secrets")
	}
	var stored string
	s.db.DB.QueryRow("SELECT data FROM registrations").Scan(&stored)
	if strings.Contains(stored, "discard-this-password") || !strings.Contains(stored, "private-refresh-token") {
		t.Fatal("mailbox password retained or OAuth lost")
	}
}
func TestInterruptedSubmissionsDoNotCreateDuplicateAccounts(t *testing.T) {
	s := testService(t)
	_, e := s.Import(mailboxFixture)
	if e != nil {
		t.Fatal(e)
	}
	rows, _ := s.loadAll()
	r := rows[0]
	r.Status = "running"
	r.State.Phase = "verification_pending"
	if e = s.save(r); e != nil {
		t.Fatal(e)
	}
	other, e := New(s.db, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	items, _, _ := other.List(1, 25)
	if items[0].Status != "attention" {
		t.Fatal("uncertain verification automatically replayed")
	}
	if e = other.Retry(r.ID); e == nil {
		t.Fatal("uncertain verification retry allowed")
	}
	r.State.Phase = "await_email"
	r.Status = "running"
	s.save(r)
	third, e := New(s.db, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer third.Close()
	items, _, _ = third.List(1, 25)
	if items[0].Status != "queued" {
		t.Fatal("safe email polling did not resume")
	}
}
func TestRegistrationHistoryPersistsAndPagination(t *testing.T) {
	s := testService(t)
	for i := 0; i < 31; i++ {
		email := string(rune('a'+i)) + "user@outlook.com"
		r := request{Summary: Summary{ID: storage.ID(), Email: email, Status: "ready", CreatedAt: time.Now().AddDate(-10, 0, 0)}, State: eva.RegistrationState{Phase: "created"}}
		b, _ := json.Marshal(r)
		if _, e := s.db.DB.Exec("INSERT INTO registrations VALUES(?,?,?,?)", r.ID, email, b, int64(i)); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.db.Prune(); e != nil {
		t.Fatal(e)
	}
	rows, total, e := s.List(2, 25)
	if e != nil || total != 31 || len(rows) != 6 {
		t.Fatal(total, len(rows), e)
	}
}

func TestBatchProgressOnlyIncludesRequestedImports(t *testing.T) {
	s := testService(t)
	first, e := s.Import(mailboxFixture)
	if e != nil || len(first.IDs) != 1 {
		t.Fatal("missing batch IDs")
	}
	second, e := s.Import(strings.Replace(mailboxFixture, "test@", "another@", 1))
	if e != nil {
		t.Fatal(e)
	}
	items, e := s.Current(first.IDs)
	if e != nil || len(items) != 1 || items[0].ID != first.IDs[0] || items[0].ID == second.IDs[0] {
		t.Fatal("unrelated history entered current batch")
	}
	if _, e = s.Current(nil); e == nil {
		t.Fatal("empty batch scanned history")
	}
}
