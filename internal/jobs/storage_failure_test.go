package jobs

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

type unreliableHistory struct {
	mu   sync.Mutex
	fail bool
	data map[string][]byte
}

func (p *unreliableHistory) SaveJob(id string, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail {
		return errors.New("disk unavailable")
	}
	p.data[id] = append([]byte(nil), data...)
	return nil
}
func (p *unreliableHistory) Jobs() ([]json.RawMessage, error) { return nil, nil }
func (p *unreliableHistory) LoadJob(id string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.data[id]...), nil
}

func TestHistoryRetriesLatestSnapshotAndRecoversAdmission(t *testing.T) {
	store := &unreliableHistory{fail: true, data: make(map[string][]byte)}
	w := newHistoryWriter(store)
	defer w.close()
	w.save(Job{ID: "job", Status: "running"})
	w.sync()
	if !w.overloaded() {
		t.Fatal("failed storage did not pause admission")
	}
	w.save(Job{ID: "job", Status: "completed"})
	w.sync()
	store.mu.Lock()
	store.fail = false
	store.mu.Unlock()
	w.sync()
	if w.overloaded() {
		t.Fatal("recovered storage still blocks admission")
	}
	data, _ := store.LoadJob("job")
	var saved Job
	if json.Unmarshal(data, &saved) != nil || saved.Status != "completed" {
		t.Fatal("failed older snapshot replaced newer terminal state")
	}
}
