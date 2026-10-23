package registration

import "encoding/json"

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

		select {
		case <-s.ctx.Done():
			return
		case <-changed:
		}
	}
}
