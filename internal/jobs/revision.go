package jobs

import "strconv"

func (j Job) ETag() string {
	return `"` + j.ID + "-" + strconv.FormatInt(j.UpdatedAt.UnixNano(), 36) + "-" + j.Status + `"`
}

func (s *Service) Unchanged(id, tag string) bool {
	if tag == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.items[id]
	return job != nil && job.ETag() == tag
}
