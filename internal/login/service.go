package login

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"awarddesk/internal/award"
)

type Check func(context.Context, award.Reporter) error
type Status struct {
	State          string           `json:"state"`
	Message        string           `json:"message"`
	ID             uint64           `json:"id"`
	StartedAt      time.Time        `json:"startedAt,omitempty"`
	FinishedAt     time.Time        `json:"finishedAt,omitempty"`
	DurationMS     int64            `json:"durationMs,omitempty"`
	Stage          string           `json:"stage,omitempty"`
	Code           string           `json:"code,omitempty"`
	OutcomeUnknown bool             `json:"outcomeUnknown,omitempty"`
	Recoveries     []award.Recovery `json:"recoveries,omitempty"`
}
type Service struct {
	mu      sync.Mutex
	check   Check
	name    string
	timeout time.Duration
	status  Status
	cancel  context.CancelFunc
	next    time.Time
	closed  bool
	wg      sync.WaitGroup
}

func New(check Check) *Service {
	return &Service{name: "EVA", timeout: 6 * time.Minute, check: check, status: Status{State: "idle", Message: "Not tested in this server session"}}
}
func (s *Service) Ready() bool { return s.check != nil }
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status
	status.Recoveries = append([]award.Recovery(nil), status.Recoveries...)
	return status
}
func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startLocked()
}
func (s *Service) startLocked() error {
	if s.closed || s.check == nil {
		return award.Failure("configuration", "Configure "+s.name+" credentials and solver keys, then restart")
	}
	if s.status.State == "running" {
		return nil
	}
	if time.Now().Before(s.next) {
		if s.status.State == "verified" {
			return nil
		}
		return award.Failure("cooldown", "Please wait before starting another "+s.name+" login check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	s.cancel = cancel
	s.status = Status{ID: s.status.ID + 1, StartedAt: time.Now(), State: "running", Message: "Preparing " + s.name + " session"}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		err := s.check(ctx, func(p award.Progress) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.status.Message = p.Message
			s.status.Stage = p.Stage
			if p.Recovery != nil {
				s.status.Recoveries = append(s.status.Recoveries, *p.Recovery)
			}
		})
		s.mu.Lock()
		defer s.mu.Unlock()
		s.cancel = nil
		s.status.FinishedAt = time.Now()
		s.status.DurationMS = time.Since(s.status.StartedAt).Milliseconds()
		s.next = time.Now().Add(5 * time.Second)
		defer func() {
			slog.Info("login check finished", "provider", s.name, "status", s.status.State, "code", s.status.Code, "stage", s.status.Stage, "durationMs", s.status.DurationMS)
		}()
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			s.status.State, s.status.Message = "cancelled", s.name+" login check cancelled"
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			s.status.State, s.status.Message = "failed", s.name+" login check exceeded its deadline"
			var ae *award.Error
			if errors.As(err, &ae) {
				s.status.Message, s.status.Code, s.status.Stage, s.status.OutcomeUnknown = ae.Message, ae.Code, ae.Stage, ae.OutcomeUnknown
			}
		case err != nil:
			s.status.State, s.status.Message = "failed", s.name+" login failed"
			var ae *award.Error
			if errors.As(err, &ae) {
				s.status.Message = ae.Message
				s.status.Code, s.status.Stage, s.status.OutcomeUnknown = ae.Code, ae.Stage, ae.OutcomeUnknown
				if ae.Code == "mfa_required" {
					s.status.State = "otp_required"
					s.status.Message = "EVA verification requires attention; check the account's Outlook configuration."
				}
				if ae.Code == "authentication" || ae.Code == "mfa_required" || ae.Code == "login_response" {
					s.next = time.Now().Add(time.Minute)
				}
				if until := time.Now().Add(time.Duration(ae.RetryAfterSeconds) * time.Second); until.After(s.next) {
					s.next = until
				}
			}
		default:
			s.status.State, s.status.Message = "verified", s.name+" session ready and saved for reuse. No flight search performed."
			s.next = time.Now().Add(15 * time.Second)
		}
	}()
	return nil
}
func (s *Service) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}
