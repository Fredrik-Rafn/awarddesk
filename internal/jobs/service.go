// Package jobs runs bounded, cancellable searches without concurrent account use.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"awarddesk/internal/award"
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
	StartedAt  *time.Time       `json:"startedAt,omitempty"`
	FinishedAt *time.Time       `json:"finishedAt,omitempty"`
	Timeline   []StageEvent     `json:"timeline,omitempty"`
	cancel     context.CancelFunc
	ctx        context.Context
	queueTimer *time.Timer
	runTimer   *time.Timer
}

// StageEvent records provider progress without credentials, headers or response bodies.
type StageEvent struct {
	At        time.Time       `json:"at"`
	Stage     string          `json:"stage"`
	Completed int             `json:"completed,omitempty"`
	Total     int             `json:"total,omitempty"`
	Recovery  *award.Recovery `json:"recovery,omitempty"`
}
type Service struct {
	mu          sync.Mutex
	items       map[string]*Job
	providers   map[string]award.Provider
	queue       []string
	changed     chan struct{}
	limit       int
	active      int
	queueWait   time.Duration
	runBudget   time.Duration
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	cooldown    time.Time
	persistence Persistence
	workers     int
	writer      *historyWriter
	closeOnce   sync.Once
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
	if workers > award.MaxConcurrency {
		workers = award.MaxConcurrency
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{items: map[string]*Job{}, providers: map[string]award.Provider{}, changed: make(chan struct{}), ctx: ctx, cancel: cancel, workers: workers, limit: workers, queueWait: 30 * time.Minute, runBudget: 15 * time.Minute}
	for _, p := range providers {
		s.providers[p.Capabilities().ID] = p
	}
	for i := 0; i < workers; i++ {
		s.wg.Add(1)
		go s.work()
	}
	return s
}

// Apply dispatch capacity live. Lowering the limit lets active operations finish
// and delays new dispatches until capacity is available.
func (s *Service) EnsureWorkers(limit int) {
	if limit < 1 {
		limit = 1
	}
	if limit > award.MaxConcurrency {
		limit = award.MaxConcurrency
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return
	}
	s.limit = limit
	s.notifyLocked()
	for s.workers < limit {
		s.workers++
		s.wg.Add(1)
		go s.work()
	}
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
	// Cache I/O must not hold the scheduler lock or block unrelated progress/cancel.
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return Job{}, award.Failure("closed", "search service is shutting down")
	}
	if existing := s.inflightLocked(q); existing != nil {
		out := cloneJob(existing)
		s.mu.Unlock()
		return out, nil
	}
	s.mu.Unlock()
	var cachedResult award.Result
	var cacheHit bool
	if cached, ok := p.(interface {
		CachedSearch(award.Query) (award.Result, bool)
	}); ok {
		cachedResult, cacheHit = cached.CachedSearch(q)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return Job{}, award.Failure("closed", "search service is shutting down")
	}
	if existing := s.inflightLocked(q); existing != nil {
		return cloneJob(existing), nil
	}
	s.trimLocked()
	now := time.Now().UTC()
	if s.writer != nil && s.writer.overloaded() {
		return Job{}, &award.Error{Code: "storage_unavailable", Message: "Search storage is temporarily busy; please try again shortly", RetryAfterSeconds: 5}
	}
	if cacheHit {
		for _, existing := range s.items {
			if existing.Query == q && existing.Status == "completed" && existing.Result != nil && existing.Result.Cached && existing.Result.FetchedAt.Equal(cachedResult.FetchedAt) && existing.Result.CacheExpiresAt != nil && cachedResult.CacheExpiresAt != nil && existing.Result.CacheExpiresAt.Equal(*cachedResult.CacheExpiresAt) {
				return cloneJob(existing), nil
			}
		}
		j := &Job{ID: id, Query: q, Status: "completed", CreatedAt: now, UpdatedAt: now, Result: &cachedResult, Progress: award.Progress{Stage: "cached results", Message: "Recent complete results reused"}}
		finished := now
		j.FinishedAt = &finished
		s.items[id] = j
		s.persist(j)
		return cloneJob(j), nil
	}
	if !managesCooldowns(p) && now.Before(s.cooldown) {
		return Job{}, &award.Error{Code: "rate_limited", Message: "provider cooldown is active", RetryAfterSeconds: int(time.Until(s.cooldown).Seconds()) + 1}
	}
	pending := 0
	for _, job := range s.items {
		if job.Status == "queued" {
			pending++
		}
	}
	if pending >= maxQueuedSearches {
		return Job{}, &award.Error{Code: "queue_full", Message: "Search capacity is temporarily full; please try again shortly", RetryAfterSeconds: 30}
	}
	ctx, cancel := context.WithCancel(s.ctx)
	j := &Job{ID: id, Query: q, Status: "queued", CreatedAt: now, UpdatedAt: now, Progress: award.Progress{Stage: "queued", Message: "Waiting for an available account"}, ctx: ctx, cancel: cancel}
	s.items[id] = j
	j.queueTimer = time.AfterFunc(s.queueWait, func() { s.expire(id, false) })
	s.persist(j)
	return s.admitLocked(j)
}
func (s *Service) Get(id string) (Job, bool) {
	s.mu.Lock()
	j, ok := s.items[id]
	if ok && j.Status != "running" && j.Status != "queued" && time.Since(j.UpdatedAt) > time.Hour {
		delete(s.items, id)
		ok = false
	}
	if ok {
		out := cloneJob(j)
		s.mu.Unlock()
		return out, true
	}
	store, writer := s.persistence, s.writer
	s.mu.Unlock()
	if store != nil {
		if writer != nil {
			writer.sync()
		}
		data, err := store.LoadJob(id)
		var saved Job
		if err == nil && json.Unmarshal(data, &saved) == nil {
			return saved, true
		}
	}
	return Job{}, false
}
func (s *Service) Cancel(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.items[id]
	if !ok {
		return false
	}
	if j.Status == "queued" || j.Status == "running" {
		stopTimers(j)
		j.cancel()
		j.Status = "cancelled"
		j.UpdatedAt = time.Now().UTC()
		j.Progress = award.Progress{Stage: "cancelled", Message: "Search cancelled"}
		finished := j.UpdatedAt
		j.FinishedAt = &finished
		s.removeQueuedLocked(id)
		s.persist(j)
		s.notifyLocked()
	}
	return true
}
func (s *Service) Close() {
	s.closeOnce.Do(func() {
		s.cancel()
		s.mu.Lock()
		for _, job := range s.items {
			stopTimers(job)
			if job.Status == "queued" {
				job.Status = "interrupted"
				job.UpdatedAt = time.Now().UTC()
				finished := job.UpdatedAt
				job.FinishedAt = &finished
				job.Error = &award.Error{Code: "interrupted", Message: "Server stopped before search execution"}
				s.persist(job)
			}
		}
		s.notifyLocked()
		s.mu.Unlock()
		s.wg.Wait()
		if s.writer != nil {
			s.writer.close()
		}
	})
}
func (s *Service) work() {
	defer s.wg.Done()
	for {
		id, ok := s.next()
		if !ok {
			return
		}
		s.execute(id)
		s.mu.Lock()
		s.active--
		s.notifyLocked()
		s.mu.Unlock()
	}
}
func (s *Service) execute(id string) {
	s.mu.Lock()
	j := s.items[id]
	if j == nil || j.Status != "queued" {
		s.mu.Unlock()
		return
	}
	if j.ctx.Err() != nil {
		j.Status = "failed"
		j.UpdatedAt = time.Now().UTC()
		j.Error = &award.Error{Code: "timeout", Message: "search expired while waiting in the queue"}
		j.cancel()
		stopTimers(j)
		finished := j.UpdatedAt
		j.FinishedAt = &finished
		s.persist(j)
		s.mu.Unlock()
		return
	}
	if !managesCooldowns(s.providers[j.Query.Provider]) && time.Now().Before(s.cooldown) {
		j.Status = "failed"
		j.UpdatedAt = time.Now().UTC()
		j.Error = &award.Error{Code: "rate_limited", Message: "provider cooldown is active"}
		j.cancel()
		stopTimers(j)
		finished := j.UpdatedAt
		j.FinishedAt = &finished
		s.persist(j)
		s.mu.Unlock()
		return
	}
	ctx, q := award.WithExecutionObserver(j.ctx, func() { s.started(id) }), j.Query
	s.mu.Unlock()
	if deferred, ok := s.providers[q.Provider].(interface{ DefersExecutionStart() bool }); !ok || !deferred.DefersExecutionStart() {
		s.started(id)
	}
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
				if p.Stage != j.Progress.Stage || p.Completed != j.Progress.Completed || p.Total != j.Progress.Total || p.Recovery != nil {
					event := StageEvent{At: time.Now().UTC(), Stage: p.Stage, Completed: p.Completed, Total: p.Total}
					if p.Recovery != nil {
						recovery := *p.Recovery
						event.Recovery = &recovery
					}
					j.Timeline = append(j.Timeline, event)
				}
				if p.Preview != nil {
					preview := award.CloneResult(*p.Preview)
					preview.Complete = false
					j.Result = &preview
					p.Preview = nil
				}
				if p.Recovery != nil {
					j.Recoveries = append(j.Recoveries, *p.Recovery)
					recovery := *p.Recovery
					p.Recovery = &recovery
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
	defer stopTimers(j)
	defer s.persist(j)
	if j.Status != "running" && j.Status != "queued" {
		return
	}
	j.UpdatedAt = time.Now().UTC()
	finished := j.UpdatedAt
	j.FinishedAt = &finished
	defer func() {
		code := ""
		if j.Error != nil {
			code = j.Error.Code
		}
		slog.Info("search finished", "id", j.ID, "provider", q.Provider, "status", j.Status, "errorCode", code, "elapsed", time.Since(j.CreatedAt).Round(time.Second))
	}()
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
	if s.persistence != nil || s.ctx.Err() != nil {
		return errors.New("search persistence is already configured or service is closed")
	}
	rows, e := p.Jobs()
	if e != nil {
		return e
	}
	s.persistence = p
	s.writer = newHistoryWriter(p)
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
	if s.writer != nil {
		s.writer.save(cloneJob(j))
	}
}
func (s *Service) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Job{}
	for _, j := range s.items {
		out = append(out, cloneJob(j))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}
