package jobs

import (
	"time"

	"awarddesk/internal/award"
)

const maxQueuedSearches = 1024

func (s *Service) admitLocked(job *Job) (Job, error) {
	writer := s.writer
	if writer != nil {
		s.mu.Unlock()
		writer.sync()
		s.mu.Lock()
	}
	if job.Status != "queued" || job.ctx.Err() != nil {
		return cloneJob(job), nil
	}
	if writer != nil && writer.overloaded() {
		job.Status = "failed"
		job.UpdatedAt = time.Now().UTC()
		finished := job.UpdatedAt
		job.FinishedAt = &finished
		job.Error = &award.Error{Code: "storage_unavailable", Message: "Search could not be stored; no airline request was started", RetryAfterSeconds: 5}
		stopTimers(job)
		job.cancel()
		s.persist(job)
		return Job{}, job.Error
	}
	s.queue = append(s.queue, job.ID)
	s.notifyLocked()
	return cloneJob(job), nil
}

func (s *Service) notifyLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *Service) inflightLocked(q award.Query) *Job {
	for _, job := range s.items {
		if job.Query == q && (job.Status == "queued" || job.Status == "running") && job.ctx.Err() == nil {
			return job
		}
	}
	return nil
}

func (s *Service) trimLocked() {
	for id, job := range s.items {
		if job.Status != "queued" && job.Status != "running" && time.Since(job.UpdatedAt) > time.Hour {
			delete(s.items, id)
		}
	}
	limit := 1000
	if s.persistence != nil {
		limit = 256
	}
	for {
		finished, oldest := 0, ""
		for id, job := range s.items {
			if job.Status == "queued" || job.Status == "running" {
				continue
			}
			finished++
			if oldest == "" || job.UpdatedAt.Before(s.items[oldest].UpdatedAt) {
				oldest = id
			}
		}
		if finished < limit {
			return
		}
		delete(s.items, oldest)
	}
}

func (s *Service) removeQueuedLocked(id string) {
	for i, queued := range s.queue {
		if queued == id {
			copy(s.queue[i:], s.queue[i+1:])
			s.queue[len(s.queue)-1] = ""
			s.queue = s.queue[:len(s.queue)-1]
			return
		}
	}
}

func stopTimers(job *Job) {
	if job.queueTimer != nil {
		job.queueTimer.Stop()
	}
	if job.runTimer != nil {
		job.runTimer.Stop()
	}
}

func (s *Service) expire(id string, executing bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.items[id]
	if job == nil || job.Status != "queued" && job.Status != "running" || executing != (job.StartedAt != nil) {
		return
	}
	code, message := "queue_timeout", "Search could not acquire capacity before its queue deadline"
	if executing {
		code, message = "timeout", "Search exceeded its execution time limit"
	}
	job.Status, job.Error = "failed", &award.Error{Code: code, Message: message}
	job.UpdatedAt = time.Now().UTC()
	finished := job.UpdatedAt
	job.FinishedAt = &finished
	job.Progress = award.Progress{Stage: code, Message: message}
	stopTimers(job)
	job.cancel()
	s.removeQueuedLocked(id)
	s.persist(job)
	s.notifyLocked()
}

func (s *Service) started(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.items[id]
	if job == nil || job.Status != "queued" || job.ctx.Err() != nil {
		return
	}
	if job.queueTimer != nil {
		job.queueTimer.Stop()
	}
	now := time.Now().UTC()
	job.Status, job.StartedAt, job.UpdatedAt = "running", &now, now
	job.runTimer = time.AfterFunc(s.runBudget, func() { s.expire(id, true) })
	s.persist(job)
}

func (s *Service) next() (string, bool) {
	for {
		s.mu.Lock()
		if s.ctx.Err() != nil {
			s.mu.Unlock()
			return "", false
		}
		if len(s.queue) > 0 && s.active < s.limit {
			id := s.queue[0]
			s.queue[0] = ""
			s.queue = s.queue[1:]
			s.active++
			s.mu.Unlock()
			return id, true
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-s.ctx.Done():
			return "", false
		case <-changed:
		}
	}
}
