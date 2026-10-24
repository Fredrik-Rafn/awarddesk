package fleet

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func setSearchActivity(t *testing.T, f *Fleet, at time.Time) {
	t.Helper()
	raw, _ := json.Marshal(map[string]int64{"lastSubmittedAt": at.UnixMilli()})
	if err := f.db.Runtime("search:last-submission", json.RawMessage(raw)); err != nil {
		t.Fatal(err)
	}
}

func waitPreparationIdle(t *testing.T, f *Fleet) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		pending := len(f.pending)
		f.mu.Unlock()
		if pending == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("preparation did not finish")
}

func TestMaintenanceIdleBoundaryAndRestart(t *testing.T) {
	f, _ := setup(t, 1)
	at := time.Now().UTC().Truncate(time.Millisecond)
	setSearchActivity(t, f, at)
	if !f.maintenanceActive(at.Add(time.Hour - time.Millisecond)) {
		t.Fatal("upkeep paused before idle deadline")
	}
	if f.maintenanceActive(at.Add(time.Hour)) || f.maintenanceActive(at.Add(2*time.Hour)) {
		t.Fatal("upkeep remains active after idle deadline")
	}
	setSearchActivity(t, f, at.Add(-2*time.Hour))
	restarted := New(f.db)
	defer restarted.Close()
	if restarted.maintenanceActive(at) {
		t.Fatal("restart renewed idle activity")
	}
}

func TestIdleMaintenanceSkipsAccountsAndSearchResumesIt(t *testing.T) {
	f, p := setup(t, 1)
	_ = f.db.SetSettings(map[string]string{"HYPER_API_KEY": "test", "ANYSOLVER_API_KEY": "test", "PROXY_LIST": "test"})
	at := time.Now().UTC().Truncate(time.Millisecond)
	old := at.Add(-2 * time.Hour)
	setSearchActivity(t, f, old)
	p["a"].info.Ready = false
	f.maintainAt(at)
	if p["a"].calls.Load() != 0 {
		t.Fatal("idle upkeep called the provider")
	}
	_, _ = f.Statuses()
	_ = f.Ready()
	if last, _ := f.db.SearchActivity(); !last.Equal(old) {
		t.Fatal("status polling renewed search activity")
	}
	f.SearchSubmitted(at)
	select {
	case <-f.activityWake:
	default:
		t.Fatal("resumed search did not wake maintenance")
	}
	f.maintainAt(at)
	waitPreparationIdle(t, f)
	if p["a"].calls.Load() != 1 {
		t.Fatal("search failed to resume account upkeep")
	}
}

func TestIdleAutomaticPrepareStopsButManualPrepareWorks(t *testing.T) {
	f, p := setup(t, 1)
	old := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	setSearchActivity(t, f, old)
	if err := f.schedulePrepare("a", false); err != nil {
		t.Fatal(err)
	}
	waitPreparationIdle(t, f)
	if p["a"].calls.Load() != 0 {
		t.Fatal("queued automatic upkeep ran while idle")
	}
	if err := f.Prepare(context.Background(), "a", nil, true); err != nil {
		t.Fatal(err)
	}
	if p["a"].calls.Load() != 1 {
		t.Fatal("manual preparation was blocked by idle gate")
	}
	if last, _ := f.db.SearchActivity(); !last.Equal(old) {
		t.Fatal("manual preparation incorrectly counts as a flight search")
	}
}

func TestIdleRestartPreservesManualVersusAutomaticPreparation(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "manual"}[manual], func(t *testing.T) {
			f, p := setup(t, 1)
			setSearchActivity(t, f, time.Now().Add(-2*time.Hour))
			_ = f.db.Runtime("account:a", Status{ID: "a", State: "queued", Stage: "queued preparation", ManualPreparation: manual})
			f.Start()
			waitPreparationIdle(t, f)
			want := int32(0)
			if manual {
				want = 1
			}
			if p["a"].calls.Load() != want {
				t.Fatalf("provider calls=%d manual=%v", p["a"].calls.Load(), manual)
			}
		})
	}
}
