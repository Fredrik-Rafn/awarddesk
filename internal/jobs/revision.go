package jobs

import "strconv"

// ETag identifies the public snapshot, not any account/session data.
func (j Job) ETag() string {
	return `"` + j.ID + "-" + strconv.FormatInt(j.UpdatedAt.UnixNano(), 36) + "-" + j.Status + `"`
}

// Unchanged avoids cloning a potentially large live result on status-only polls.
func (s *Service) Unchanged(id, tag string) bool {
	if tag == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.items[id]
	return job != nil && job.ETag() == tag
}
