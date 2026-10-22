package registration

import "encoding/json"

// Call with mu held. Wake every waiting worker after new work or a limit change.
// Replacing the channel lets the next group of idle workers wait again.
func (s *Service) notifyLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
}

// Claim one row while holding mu, so two workers cannot start the same signup.
// Return the wake channel under the same lock to avoid missing a new import.
func (s *Service) next(index int) (request, <-chan struct{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := s.changed
	if s.closed || index >= s.limit {
		return request{}, changed, false
	}
	var data []byte
	err := s.db.DB.QueryRow("SELECT data FROM registrations WHERE json_extract(data,'$.status')='queued' ORDER BY created_at,id LIMIT 1").Scan(&data)
	if err != nil {
		return request{}, changed, false
	}
	var r request
	if json.Unmarshal(data, &r) != nil {
		return request{}, changed, false
	}
	r.Status, r.Stage = "running", "waiting for worker"
	if s.save(r) != nil {
		return request{}, changed, false
	}
	return r, changed, true
}

func (s *Service) worker(index int) {
	defer s.wg.Done()
	for s.ctx.Err() == nil {
		r, changed, ok := s.next(index)
		if ok {
			s.run(r)
			continue
		}
		// Imports/retries wake us immediately. The existing 15-second upkeep
		// tick also wakes us, so a temporary database failure can recover.
		select {
		case <-s.ctx.Done():
			return
		case <-changed:
		}
	}
}
