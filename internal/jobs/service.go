// Package jobs runs bounded, cancellable searches without concurrent account use.
package jobs

import (
	"awarddesk/internal/award"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"
)

type Job struct {
	ID         string           `json:"id"`
	Query      award.Query      `json:"query"`
	Status     string           `json:"status"`
	CreatedAt  time.Time        `json:"createdAt"`
	UpdatedAt  time.Time        `json:"updatedAt"`
	Progress   award.Progress   `json:"progress"`
	Result     *award.Result    `json:"result,omitempty"`
	Error      *award.Error     `json:"error,omitempty"`
	Recoveries []award.Recovery `json:"recoveries,omitempty"`
	cancel     context.CancelFunc
	ctx        context.Context
}
type Service struct {
	mu          sync.Mutex
	items       map[string]*Job
	providers   map[string]award.Provider
	queue       chan string
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	cooldown    time.Time
	persistence Persistence
}

type Persistence interface {
	SaveJob(string, []byte) error
	LoadJob(string) ([]byte, error)
	Jobs() ([]json.RawMessage, error)
}

func New(providers ...award.Provider) *Service { return NewConcurrent(1, providers...) }
func NewConcurrent(workers int, providers ...award.Provider) *Service {
	if workers < 1 {
		workers = 1
	}
	if workers > 16 {
		workers = 16
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{items: map[string]*Job{}, providers: map[string]award.Provider{}, queue: make(chan string, 64), ctx: ctx, cancel: cancel}
	for _, p := range providers {
		s.providers[p.Capabilities().ID] = p
	}
	for i := 0; i < workers; i++ {
		s.wg.Add(1)
		go s.work()
	}
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
	if s.persistence != nil && len(s.items) >= 256 {
		oldest := ""
		for id, job := range s.items {
			if job.Status == "running" || job.Status == "queued" {
				continue
			}
			if oldest == "" || job.UpdatedAt.Before(s.items[oldest].UpdatedAt) {
				oldest = id
			}
		}
		if oldest != "" {
			delete(s.items, oldest)
		}
	}
	if len(s.items) >= 1000 {
		return Job{}, award.Failure("capacity", "result storage is full; retry after older searches expire")
	}
	for _, existing := range s.items {
		if existing.Query == q && (existing.Status == "queued" || existing.Status == "running") && existing.ctx.Err() == nil {
			return *existing, nil
		}
	}
	// Cached complete results bypass live-search queues and provider cooldowns.
	if cached, ok := p.(interface {
		CachedSearch(award.Query) (award.Result, bool)
	}); ok {
		if result, hit := cached.CachedSearch(q); hit {
			for _, existing := range s.items {
				if existing.Query == q && existing.Status == "completed" && existing.Result != nil && existing.Result.Cached && existing.Result.FetchedAt.Equal(result.FetchedAt) && existing.Result.CacheExpiresAt != nil && result.CacheExpiresAt != nil && existing.Result.CacheExpiresAt.Equal(*result.CacheExpiresAt) {
					return *existing, nil
				}
			}
			j := &Job{ID: id, Query: q, Status: "completed", CreatedAt: now, UpdatedAt: now, Result: &result, Progress: award.Progress{Stage: "cached results", Message: "Recent complete results reused"}}
			s.items[id] = j
			s.persist(j)
			return *j, nil
		}
	}
	if !managesCooldowns(p) && now.Before(s.cooldown) {
		return Job{}, &award.Error{Code: "rate_limited", Message: "provider cooldown is active", RetryAfterSeconds: int(time.Until(s.cooldown).Seconds()) + 1}
	}
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Minute)
	j := &Job{ID: id, Query: q, Status: "queued", CreatedAt: now, UpdatedAt: now, Progress: award.Progress{Stage: "queued", Message: "Waiting for an available account"}, ctx: ctx, cancel: cancel}
	select {
	case s.queue <- id:
		s.items[id] = j
		s.persist(j)
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
	if ok && j.Status != "running" && j.Status != "queued" && time.Since(j.UpdatedAt) > time.Hour {
		delete(s.items, id)
		ok = false
	}
	if !ok {
		if s.persistence != nil {
			b, e := s.persistence.LoadJob(id)
			var saved Job
			if e == nil && json.Unmarshal(b, &saved) == nil {
				return saved, true
			}
		}
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
		s.persist(j)
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
	if !managesCooldowns(s.providers[j.Query.Provider]) && time.Now().Before(s.cooldown) {
		j.Status = "failed"
		j.UpdatedAt = time.Now().UTC()
		j.Error = &award.Error{Code: "rate_limited", Message: "provider cooldown is active"}
		j.cancel()
		s.mu.Unlock()
		return
	}
	j.Status = "running"
	j.UpdatedAt = time.Now().UTC()
	s.persist(j)
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
				if p.Preview != nil {
					preview := award.CloneResult(*p.Preview)
					preview.Complete = false
					j.Result = &preview
					p.Preview = nil
				}
				if p.Recovery != nil {
					j.Recoveries = append(j.Recoveries, *p.Recovery)
				}
				j.Progress = p
				j.UpdatedAt = time.Now().UTC()
				s.persist(j)
			}
		})
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	defer j.cancel()
	defer s.persist(j)
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
		// Keep validated flight data and any finished quotes when a later
		// pricing step fails. The job remains failed, never a full success.
		if len(result.Itineraries) > 0 {
			result.Complete = false
			j.Result = &result
		}
		j.Status = "failed"
		if errors.Is(err, context.Canceled) {
			j.Status = "cancelled"
		} else if errors.Is(err, context.DeadlineExceeded) {
			j.Error = &award.Error{Code: "timeout", Message: "search exceeded its time limit"}
		} else {
			var ae *award.Error
			if errors.As(err, &ae) {
				j.Error = ae
				if (ae.Code == "rate_limited" || ae.Code == "site_unavailable" || ae.Code == "login_rate_limited") && !managesCooldowns(s.providers[q.Provider]) {
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

func managesCooldowns(p award.Provider) bool {
	v, ok := p.(interface{ ManagesCooldowns() bool })
	return ok && v.ManagesCooldowns()
}

// Persistence is attached before HTTP serving. Interrupted jobs never replay checkout.
func (s *Service) SetPersistence(p Persistence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := p.Jobs()
	if e != nil {
		return e
	}
	s.persistence = p
	for _, b := range rows {
		var j Job
		if json.Unmarshal(b, &j) != nil {
			continue
		}
		if j.Status == "running" || j.Status == "queued" {
			j.Status = "interrupted"
			j.UpdatedAt = time.Now().UTC()
			j.Error = &award.Error{Code: "interrupted", Message: "Server restarted; start a new search", OutcomeUnknown: true}
			s.persist(&j)
		}
	}
	return nil
}
func (s *Service) persist(j *Job) {
	if s.persistence == nil {
		return
	}
	b, e := json.Marshal(j)
	if e == nil {
		e = s.persistence.SaveJob(j.ID, b)
	}
	if e != nil {
		slog.Error("search history could not be saved")
	}
}
func (s *Service) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Job{}
	for _, j := range s.items {
		v := *j
		if j.Result != nil {
			r := award.CloneResult(*j.Result)
			v.Result = &r
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}
