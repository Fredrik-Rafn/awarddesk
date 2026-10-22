package fleet

import (
	"errors"
	"time"

	"awarddesk/internal/award"
)

type prepareTask struct {
	id    string
	force bool
}
type PrepareBatch struct {
	Queued       int `json:"queued"`
	Skipped      int `json:"skipped"`
	Unconfigured int `json:"unconfigured"`
}

func (f *Fleet) enqueuePrepare(id string, force bool) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return false, errors.New("service is shutting down")
	}
	if f.pending[id] {
		return false, nil
	}
	if en := f.entries[id]; en != nil && en.busy {
		return false, errors.New("account is busy")
	}
	if err := f.db.Runtime("account:"+id, Status{ID: id, State: "queued", Stage: "queued preparation", Message: "Waiting for an available session worker", CheckedAt: time.Now().UTC(), Queued: true}); err != nil {
		return false, errors.New("could not queue account preparation")
	}
	f.pending[id] = true
	f.prepareQueue = append(f.prepareQueue, prepareTask{id, force})
	f.growPrepareWorkersLocked()
	select {
	case f.prepareWake <- struct{}{}:
	default:
	}
	return true, nil
}

func (f *Fleet) growPrepareWorkersLocked() {
	limit := award.ConcurrencyLimit(f.db.Setting("SEARCH_CONCURRENCY"))
	if len(f.pending) < limit {
		limit = len(f.pending)
	}
	for f.prepareWorkers < limit {
		f.prepareWorkers++
		f.wg.Add(1)
		go f.prepareWorker()
	}
}
func (f *Fleet) RefreshPreparationWorkers() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.growPrepareWorkersLocked()
	}
}
func (f *Fleet) prepareWorker() {
	defer f.wg.Done()
	for {
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			return
		}
		if len(f.prepareQueue) == 0 {
			f.mu.Unlock()
			select {
			case <-f.ctx.Done():
				return
			case <-f.prepareWake:
			}
			continue
		}
		task := f.prepareQueue[0]
		f.prepareQueue = f.prepareQueue[1:]
		f.mu.Unlock()
		_ = f.Prepare(f.ctx, task.id, nil, task.force)
		f.mu.Lock()
		delete(f.pending, task.id)
		f.mu.Unlock()
	}
}
func (f *Fleet) RequestPrepareAll() (PrepareBatch, error) {
	rows, err := f.db.Accounts()
	if err != nil {
		return PrepareBatch{}, err
	}
	states, err := f.Statuses()
	if err != nil {
		return PrepareBatch{}, err
	}
	byID := map[string]Status{}
	for _, st := range states {
		byID[st.ID] = st
	}
	result := PrepareBatch{}
	for _, a := range rows {
		if !a.Enabled {
			result.Skipped++
			continue
		}
		if !configured(a) {
			result.Unconfigured++
			continue
		}
		st := byID[a.ID]
		if st.Busy || st.Queued || (st.State == "ready" && st.Session.Ready && !st.NeedsAttention) {
			result.Skipped++
			continue
		}
		added, e := f.enqueuePrepare(a.ID, true)
		if e != nil {
			return result, e
		}
		if added {
			result.Queued++
		} else {
			result.Skipped++
		}
	}
	return result, nil
}
