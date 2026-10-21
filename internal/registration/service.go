// Package registration owns durable bulk EVA onboarding. Secrets and provider
// checkpoints stay in SQLite; the public view contains progress only.
package registration

import (
	"awarddesk/internal/award"
	"awarddesk/internal/fleet"
	"awarddesk/internal/outlook"
	"awarddesk/internal/providers/eva"
	"awarddesk/internal/session"
	"awarddesk/internal/storage"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Summary struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Status    string    `json:"status"`
	Stage     string    `json:"stage"`
	Message   string    `json:"message"`
	Error     string    `json:"error,omitempty"`
	AccountID string    `json:"accountId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
type request struct {
	Summary
	ClientID     string                `json:"clientId"`
	RefreshToken string                `json:"refreshToken"`
	State        eva.RegistrationState `json:"state"`
}
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

type Service struct {
	db              *storage.Store
	fleet           *fleet.Fleet
	ctx             context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	wg              sync.WaitGroup
	started, closed bool
}

func New(db *storage.Store, f *fleet.Fleet) (*Service, error) {
	_, err := db.DB.Exec(`CREATE TABLE IF NOT EXISTS registrations(id TEXT PRIMARY KEY,email TEXT UNIQUE COLLATE NOCASE NOT NULL,data BLOB NOT NULL,created_at INTEGER NOT NULL);`)
	if err != nil {
		return nil, err
	}
	if _, err = db.DB.Exec(`CREATE INDEX IF NOT EXISTS registrations_status ON registrations(json_extract(data,'$.status'),created_at); CREATE INDEX IF NOT EXISTS registrations_created ON registrations(created_at DESC,id);`); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{db: db, fleet: f, ctx: ctx, cancel: cancel}
	rows, err := s.loadAll()
	if err != nil {
		cancel()
		return nil, err
	}
	for _, r := range rows {
		if r.Status == "running" {
			r.Status = "queued"
			r.Stage = "resuming"
			r.Message = "Resuming the saved registration checkpoint"
			if r.State.Phase == "verification_pending" || r.State.Phase == "email_login_pending" {
				r.Status = "attention"
				r.Message = "A verification submission was interrupted; inspect the account before retrying"
			}
			if err = s.save(r); err != nil {
				cancel()
				return nil, err
			}
		}
	}
	return s, nil
}
func (s *Service) save(r request) error {
	r.UpdatedAt = time.Now().UTC()
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	_, e = s.db.DB.Exec("UPDATE registrations SET data=? WHERE id=?", b, r.ID)
	return e
}
func (s *Service) loadAll() ([]request, error) {
	rows, e := s.db.DB.Query("SELECT data FROM registrations ORDER BY created_at DESC,id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []request{}
	for rows.Next() {
		var b []byte
		var r request
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &r); e != nil {
			return nil, e
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

type ImportResult struct {
	Added   int `json:"added"`
	Skipped int `json:"skipped"`
}

func (s *Service) Import(text string) (ImportResult, error) {
	boxes, err := ParseMailboxes(text)
	if err != nil {
		return ImportResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ImportResult{}, errors.New("Registration service is closed")
	}
	tx, err := s.db.DB.Begin()
	if err != nil {
		return ImportResult{}, err
	}
	defer tx.Rollback()
	result := ImportResult{}
	for _, box := range boxes {
		var n int
		if err = tx.QueryRow("SELECT (SELECT count(*) FROM registrations WHERE email=?)+(SELECT count(*) FROM accounts WHERE email=?)", box.Email, box.Email).Scan(&n); err != nil {
			return result, err
		}
		if n > 0 {
			result.Skipped++
			continue
		}
		profile, e := eva.NewRegistrationProfile()
		if e != nil {
			return result, e
		}
		now := time.Now().UTC()
		r := request{Summary: Summary{ID: storage.ID(), Email: box.Email, Status: "queued", Stage: "queued", Message: "Waiting for an available worker and unused sticky proxy", CreatedAt: now, UpdatedAt: now}, ClientID: box.ClientID, RefreshToken: box.RefreshToken, State: eva.RegistrationState{Profile: profile}}
		b, e := json.Marshal(r)
		if e != nil {
			return result, e
		}
		if _, e = tx.Exec("INSERT INTO registrations VALUES(?,?,?,?)", r.ID, r.Email, b, now.UnixMilli()); e != nil {
			return result, e
		}
		result.Added++
	}
	return result, tx.Commit()
}
func (s *Service) List(page, limit int) ([]Summary, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 25
	}
	var total int
	if e := s.db.DB.QueryRow("SELECT count(*) FROM registrations").Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := s.db.DB.Query("SELECT data FROM registrations ORDER BY created_at DESC,id LIMIT ? OFFSET ?", limit, (page-1)*limit)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		var b []byte
		var r request
		if e = rows.Scan(&b); e != nil {
			return nil, 0, e
		}
		if e = json.Unmarshal(b, &r); e != nil {
			return nil, 0, e
		}
		out = append(out, r.Summary)
	}
	return out, total, rows.Err()
}
func (s *Service) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.closed {
		return
	}
	s.started = true
	for i := 0; i < 16; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if states, e := s.fleet.Statuses(); e == nil {
					s.syncReady(states)
				}
			}
		}
	}()
}

// Background login preparation can recover after the onboarding worker has
// finished. Reflect that verified session without creating/logging in again.
func (s *Service) syncReady(states []fleet.Status) {
	ready := map[string]bool{}
	for _, state := range states {
		if state.State == "ready" && state.Session.Ready && !state.NeedsAttention {
			ready[state.ID] = true
		}
	}
	if len(ready) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	rows, e := s.db.DB.Query("SELECT data FROM registrations WHERE json_extract(data,'$.status')='failed' AND json_extract(data,'$.state.phase')='created'")
	if e != nil {
		return
	}
	var recovered []request
	for rows.Next() {
		var b []byte
		var r request
		if rows.Scan(&b) == nil && json.Unmarshal(b, &r) == nil && ready[r.AccountID] {
			recovered = append(recovered, r)
		}
	}
	rows.Close()
	for _, r := range recovered {
		r.Status = "ready"
		r.Stage = "session ready"
		r.Error = ""
		r.Message = "Account created and verified; background preparation saved its reusable session"
		_ = s.save(r)
	}
}
func (s *Service) Close() { s.mu.Lock(); s.closed = true; s.cancel(); s.mu.Unlock(); s.wg.Wait() }
func (s *Service) next() (request, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return request{}, false
	}
	var b []byte
	e := s.db.DB.QueryRow("SELECT data FROM registrations WHERE json_extract(data,'$.status')='queued' ORDER BY created_at LIMIT 1").Scan(&b)
	if e != nil {
		return request{}, false
	}
	var r request
	if json.Unmarshal(b, &r) != nil {
		return r, false
	}
	r.Status = "running"
	r.Stage = "waiting for worker"
	if s.save(r) != nil {
		return r, false
	}
	return r, true
}
func (s *Service) worker() {
	defer s.wg.Done()
	for {
		if s.ctx.Err() != nil {
			return
		}
		r, ok := s.next()
		if ok {
			s.run(r)
			continue
		}
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
func (s *Service) run(r request) {
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Minute)
	defer cancel()
	report := func(p award.Progress) { r.Stage = p.Stage; r.Message = p.Message; _ = s.save(r) }
	err := s.create(ctx, &r, report)
	if r.State.Phase == "created" && retrySessionPreparation(err) && ctx.Err() == nil {
		report(award.Progress{Stage: "session preparation retry", Message: "Account is already created; retrying safe login preparation automatically"})
		err = s.create(ctx, &r, report)
	}
	for attempt := 0; err != nil && attempt < 2 && r.State.Phase == "" && ctx.Err() == nil; attempt++ {
		var failure *award.Error
		if !errors.As(err, &failure) || (failure.Code != "connection" && failure.Code != "timeout" && failure.Code != "proxy_changed" && failure.Code != "session_denied" && failure.Code != "response_read") {
			break
		}
		report(award.Progress{Stage: "registration recovery", Message: "Registration setup was interrupted before account submission; retrying on a fresh sticky proxy"})
		err = s.create(ctx, &r, report)
	}
	if err == nil {
		r.Error = ""
		r.Status = "ready"
		r.Stage = "session ready"
		r.Message = "EVA account created, email login enabled and reusable session saved"
	} else {
		r.Status = "failed"
		r.Message = "Account setup failed; safe steps can be retried"
		var ae *award.Error
		if errors.As(err, &ae) {
			r.Message = ae.Message
			r.Error = ae.Message
		}
		if strings.HasPrefix(err.Error(), "Outlook") || strings.HasPrefix(err.Error(), "no fresh EVA") {
			r.Error = err.Error()
		}
		if r.State.Phase != "" && r.State.Phase != "created" {
			r.Status = "attention"
			r.Message = "Registration may have been submitted. Resume verification; do not submit a second signup."
		}
		if r.State.Phase == "created" {
			r.Message = "Account created; saved-session preparation needs a retry"
		}
		if s.ctx.Err() != nil {
			r.Message = "Setup interrupted by server shutdown"
			if r.State.Phase == "" || r.State.Phase == "created" || r.State.Phase == "submit_pending" || r.State.Phase == "await_email" || r.State.Phase == "challenge_pending" {
				r.Status = "queued"
			}
		}
	}
	_ = s.save(r)
}
func retrySessionPreparation(err error) bool {
	var failure *award.Error
	if !errors.As(err, &failure) || failure.OutcomeUnknown || (failure.Code != "connection" && failure.Code != "response_read" && failure.Code != "session_denied") {
		return false
	}
	switch failure.Stage {
	case "proxy verification", "session verification", "login page", "captcha", "image captcha", "security", "mailbox", "adaptive security":
		return true
	}
	return false
}
func (s *Service) create(ctx context.Context, r *request, report award.Reporter) error {
	if r.State.Phase != "created" {
		settings, e := s.db.Settings()
		if e != nil {
			return e
		}
		if settings["PROXY_LIST"] == "" || settings["HYPER_API_KEY"] == "" || settings["ANYSOLVER_API_KEY"] == "" {
			return errors.New("Configure proxies and solver keys first")
		}
		var saved []string
		if r.State.Phase != "" {
			saved = []string{r.State.Template, r.State.Proxy}
		}
		lease, release, e := s.fleet.RegistrationLease(ctx, saved...)
		if e != nil {
			return e
		}
		e = func() error {
			defer release()
			c, err := session.New(lease.URL, []string{"eservice.evaair.com", "api.ipify.org"})
			if err != nil {
				return err
			}
			defer c.Close()
			if r.State.Phase != "" {
				if err = c.Restore(r.State.HTTP); err != nil {
					return err
				}
			}
			r.State.Proxy = lease.URL
			r.State.Template = lease.Template
			reader := outlook.New(outlook.Config{Email: r.Email, ClientID: r.ClientID, RefreshToken: r.RefreshToken, StateBackend: s.db})
			provider := eva.New(eva.Config{Email: r.Email, Password: r.State.Profile.Password, HyperKey: settings["HYPER_API_KEY"], AnyKey: settings["ANYSOLVER_API_KEY"], Mailbox: reader})
			return provider.Register(ctx, c, lease.URL, &r.State, func() error { return s.save(*r) }, report)
		}()
		if e != nil {
			return e
		}
	}
	if r.AccountID == "" {
		a := storage.Account{ID: r.ID, Label: r.State.Profile.FirstName + " " + r.State.Profile.LastName, Email: r.Email, Password: r.State.Profile.Password, OutlookEmail: r.Email, ClientID: r.ClientID, RefreshToken: r.RefreshToken, Enabled: true}
		if e := s.db.PutAccount(a); e != nil {
			return e
		}
		r.AccountID = a.ID
		if e := s.save(*r); e != nil {
			return e
		}
	}
	report(award.Progress{Stage: "preparing session", Message: "Account verified; preparing a reusable EVA login session"})
	return s.fleet.Prepare(ctx, r.AccountID, report, true)
}
func (s *Service) Retry(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b []byte
	if e := s.db.DB.QueryRow("SELECT data FROM registrations WHERE id=?", id).Scan(&b); e != nil {
		return errors.New("Registration not found")
	}
	var r request
	if json.Unmarshal(b, &r) != nil {
		return errors.New("Could not read registration")
	}
	if r.Status == "running" || r.Status == "queued" || r.Status == "ready" {
		return errors.New("Registration is already active or ready")
	}
	if r.State.Phase == "verification_pending" || r.State.Phase == "email_login_pending" {
		return errors.New("Verification outcome is uncertain; inspect this account before continuing")
	}
	r.Status = "queued"
	r.Stage = "queued"
	r.Error = ""
	r.Message = "Resuming safe steps from the saved checkpoint"
	return s.save(r)
}
