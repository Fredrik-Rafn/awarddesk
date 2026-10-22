package jobs

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// historyWriter coalesces superseded progress for each job. SQL and JSON
// encoding run outside the scheduler mutex; terminal snapshots are flushed on
// shutdown and before a historical read. One writer preserves per-job order.
type historyWriter struct {
	mu      sync.Mutex
	store   Persistence
	pending map[string]Job
	wake    chan struct{}
	stop    chan struct{}
	done    chan struct{}
	flush   chan chan struct{}
	failed  bool
}

func newHistoryWriter(store Persistence) *historyWriter {
	w := &historyWriter{store: store, pending: make(map[string]Job), wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}), flush: make(chan chan struct{})}
	go w.run()
	return w
}

func (w *historyWriter) save(job Job) {
	w.mu.Lock()
	w.pending[job.ID] = job
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *historyWriter) drain() {
	w.mu.Lock()
	pending := w.pending
	w.pending = make(map[string]Job)
	w.mu.Unlock()
	failed := false
	for _, job := range pending {
		data, err := json.Marshal(job)
		if err == nil {
			err = w.store.SaveJob(job.ID, data)
		}
		if err != nil {
			failed = true
			slog.Error("search history could not be saved", "id", job.ID)
			w.mu.Lock()
			if _, newer := w.pending[job.ID]; !newer {
				w.pending[job.ID] = job
			}
			w.mu.Unlock()
		}
	}
	if len(pending) > 0 {
		w.mu.Lock()
		w.failed = failed
		w.mu.Unlock()
	}
}

func (w *historyWriter) overloaded() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.failed || len(w.pending) >= 2048
}

func (w *historyWriter) run() {
	defer close(w.done)
	retry := time.NewTicker(time.Second)
	defer retry.Stop()
	for {
		select {
		case <-w.wake:
			w.drain()
		case <-retry.C:
			w.drain()
		case complete := <-w.flush:
			w.drain()
			close(complete)
		case <-w.stop:
			w.drain()
			return
		}
	}
}

func (w *historyWriter) sync() {
	complete := make(chan struct{})
	select {
	case w.flush <- complete:
		<-complete
	case <-w.done:
	}
}

func (w *historyWriter) close() {
	close(w.stop)
	<-w.done
}
