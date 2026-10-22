package registration

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestImportWakesIdleWorkersAndEachSetupIsClaimedOnce(t *testing.T) {
	s := testService(t)
	s.limit = 10
	_, wake, ok := s.next(0)
	if ok {
		t.Fatal("empty queue returned work")
	}
	if _, err := s.Import(mailboxFixture); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wake:
	default:
		t.Fatal("import did not wake idle workers")
	}
	var claims atomic.Int32
	var workers sync.WaitGroup
	for i := 0; i < 10; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			if _, _, ok := s.next(index); ok {
				claims.Add(1)
			}
		}(i)
	}
	workers.Wait()
	if claims.Load() != 1 {
		t.Fatalf("one signup was claimed %d times", claims.Load())
	}
}

func TestLoweredRegistrationLimitLeavesExcessWorkQueued(t *testing.T) {
	s := testService(t)
	if _, err := s.Import(mailboxFixture); err != nil {
		t.Fatal(err)
	}
	s.limit = 1
	if _, _, ok := s.next(1); ok {
		t.Fatal("worker above current limit claimed work")
	}
	if _, _, ok := s.next(0); !ok {
		t.Fatal("allowed worker did not claim queued work")
	}
}

func TestRetryAndRestartWakeIdleWorkers(t *testing.T) {
	for _, restart := range []bool{false, true} {
		s := testService(t)
		s.Import(mailboxFixture)
		rows, _ := s.loadAll()
		r := rows[0]
		r.Status = "failed"
		if restart {
			r.State.Phase = "await_email"
			r.State.Checkpoint.Since = r.CreatedAt.Add(-2 * time.Hour)
		}
		if err := s.save(r); err != nil {
			t.Fatal(err)
		}
		_, wake, _ := s.next(0)
		var err error
		if restart {
			err = s.Restart(r.ID)
		} else {
			err = s.Retry(r.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-wake:
		default:
			t.Fatal("resumed setup did not wake workers")
		}
	}
}
