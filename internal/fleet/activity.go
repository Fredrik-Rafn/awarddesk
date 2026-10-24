package fleet

import (
	"errors"
	"log/slog"
	"time"
)

const searchIdleTimeout = time.Hour

type automaticPreparationKey struct{}

var errMaintenanceIdle = errors.New("automatic session upkeep is paused after one hour without searches")

func (f *Fleet) initializeSearchActivity(now time.Time) {
	if err := f.db.InitializeSearchActivity(now); err != nil {
		slog.Warn("Search activity could not be initialized; automatic session upkeep remains paused")
	}
}

func (f *Fleet) SearchSubmitted(at time.Time) {
	previous, err := f.db.RecordSearchActivity(at)
	if err != nil {
		slog.Warn("Search activity could not be saved")
		return
	}
	if previous.IsZero() || !at.Before(previous.Add(searchIdleTimeout)) {
		select {
		case f.activityWake <- struct{}{}:
		default:
		}
	}
}

func (f *Fleet) maintenanceActive(now time.Time) bool {
	last, err := f.db.SearchActivity()
	return err == nil && !last.After(now) && now.Before(last.Add(searchIdleTimeout))
}
