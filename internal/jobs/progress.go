package jobs

import (
	"time"

	"awarddesk/internal/award"
)

func (s *Service) reportProgress(id string, p award.Progress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.items[id]
	if j == nil || j.Status != "running" {
		return
	}

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
