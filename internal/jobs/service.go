// Package jobs runs bounded, cancellable searches without concurrent account use.
package jobs

import (
	"awarddesk/internal/award"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"
)

type Job struct {
	ID        string         `json:"id"`
	Query     award.Query    `json:"query"`
	Status    string         `json:"status"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	Progress  award.Progress `json:"progress"`
	Result    *award.Result  `json:"result,omitempty"`
	Error     *award.Error   `json:"error,omitempty"`
	cancel    context.CancelFunc
	ctx       context.Context
}
type Service struct {
	mu        sync.Mutex
	items     map[string]*Job
	providers map[string]award.Provider
	queue     chan string
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	cooldown  time.Time
}

func New(providers ...award.Provider) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{items: map[string]*Job{}, providers: map[string]award.Provider{}, queue: make(chan string, 8), ctx: ctx, cancel: cancel}
	for _, p := range providers {
		s.providers[p.Capabilities().ID] = p
	}
	s.wg.Add(1)
	go s.work()
	return s
}
func (s *Service) Capabilities() []award.Capabilities {
	a := []award.Capabilities{}
	for _, p := range s.providers {
		a = append(a, p.Capabilities())
	}
	sort.Slice(a, func(i, j int) bool { return a[i].ID < a[j].ID })
	return a
}
func (s *Service) Submit(q award.Query) (Job, error) {
	if e := q.Validate(time.Now()); e != nil {
		return Job{}, e
	}
	p, ok := s.providers[q.Provider]
	if !ok {
		return Job{}, errors.New("unknown provider")
	}
	supported := false
	for _, c := range p.Capabilities().Cabins {
		supported = supported || c == q.Cabin
	}
	if !supported || q.Adults > p.Capabilities().MaxAdults {
		return Job{}, errors.New("search is not supported by this provider")
	}
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return Job{}, e
	}
	id := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return Job{}, award.Failure("closed", "search service is shutting down")
	}
	now := time.Now().UTC()
	for k, j := range s.items {
		if now.Sub(j.UpdatedAt) > time.Hour && j.Status != "running" && j.Status != "queued" {
			delete(s.items, k)
		}
	}
	if len(s.items) >= 100 {
		return Job{}, award.Failure("capacity", "result storage is full; retry after older searches expire")
	}
	if now.Before(s.cooldown) {
		return Job{}, &award.Error{Code: "rate_limited", Message: "provider cooldown is active", RetryAfterSeconds: int(time.Until(s.cooldown).Seconds()) + 1}
	}
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Minute)
	j := &Job{ID: id, Query: q, Status: "queued", CreatedAt: now, UpdatedAt: now, Progress: award.Progress{Stage: "queued", Message: "Waiting for the current search"}, ctx: ctx, cancel: cancel}
	select {
	case s.queue <- id:
		s.items[id] = j
		return *j, nil
	default:
		cancel()
		return Job{}, award.Failure("queue_full", "search queue is full")
	}
}
func (s *Service) Get(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.items[id]
	if !ok {
		return Job{}, false
	}
	if j.Status != "running" && j.Status != "queued" && time.Since(j.UpdatedAt) > time.Hour {
		delete(s.items, id)
		return Job{}, false
	}
	return *j, true
}
func (s *Service) Cancel(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.items[id]
	if !ok {
		return false
	}
	if j.Status == "queued" || j.Status == "running" {
		j.cancel()
		j.Status = "cancelled"
		j.UpdatedAt = time.Now().UTC()
		j.Progress = award.Progress{Stage: "cancelled", Message: "Search cancelled"}
	}
	return true
}
func (s *Service) Close() { s.cancel(); s.wg.Wait() }
func (s *Service) work() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case id := <-s.queue:
			s.execute(id)
		}
	}
}
func (s *Service) execute(id string) {
	s.mu.Lock()
	j := s.items[id]
	if j == nil || j.Status == "cancelled" {
		s.mu.Unlock()
		return
	}
	if j.ctx.Err() != nil {
		j.Status = "failed"
		j.UpdatedAt = time.Now().UTC()
		j.Error = &award.Error{Code: "timeout", Message: "search expired while waiting in the queue"}
		j.cancel()
		s.mu.Unlock()
		return
	}
	if time.Now().Before(s.cooldown) {
		j.Status = "failed"
		j.UpdatedAt = time.Now().UTC()
		j.Error = &award.Error{Code: "rate_limited", Message: "provider cooldown is active"}
		j.cancel()
		s.mu.Unlock()
		return
	}
	j.Status = "running"
	j.UpdatedAt = time.Now().UTC()
	ctx, q := j.ctx, j.Query
	s.mu.Unlock()
	var result award.Result
	var err error
	func() {
		defer func() {
			if recover() != nil {
				err = award.Failure("internal", "provider failed unexpectedly")
			}
		}()
		result, err = s.providers[q.Provider].Search(ctx, q, func(p award.Progress) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if j.Status == "running" {
				j.Progress = p
				j.UpdatedAt = time.Now().UTC()
			}
		})
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	defer j.cancel()
	j.UpdatedAt = time.Now().UTC()
	defer func() {
		code := ""
		if j.Error != nil {
			code = j.Error.Code
		}
		slog.Info("search finished", "id", j.ID, "provider", q.Provider, "status", j.Status, "errorCode", code, "elapsed", time.Since(j.CreatedAt).Round(time.Second))
	}()
	if j.Status == "cancelled" {
		return
	}
	if err != nil {
		j.Status = "failed"
		if errors.Is(err, context.Canceled) {
			j.Status = "cancelled"
		} else if errors.Is(err, context.DeadlineExceeded) {
			j.Error = &award.Error{Code: "timeout", Message: "search exceeded its time limit"}
		} else {
			var ae *award.Error
			if errors.As(err, &ae) {
				j.Error = ae
				if ae.Code == "rate_limited" || ae.Code == "site_unavailable" || ae.Code == "login_rate_limited" {
					s.cooldown = time.Now().Add(time.Duration(ae.RetryAfterSeconds) * time.Second)
				}
			} else {
				j.Error = &award.Error{Code: "provider_error", Message: "provider could not complete this search; check configuration or retry later"}
			}
		}
		return
	}
	j.Status = "completed"
	j.Result = &result
	j.Progress = award.Progress{Stage: "completed", Message: "Search complete", Completed: len(result.Itineraries), Total: len(result.Itineraries)}
}
