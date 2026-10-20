package login

import (
	"context"
	"time"
)

// Maintain runs in the application process. Failure, OTP, cancellation and
// uncertain outcomes pause maintenance until the user explicitly starts again.
// Missing/expired sessions are handled by the configured check (ANA Manager).
func (s *Service) Maintain(ctx context.Context) {
	s.maintain(ctx, 5*time.Minute)
}
func (s *Service) maintain(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		if !s.closed && (s.status.State == "idle" || s.status.State == "verified") {
			_ = s.startLocked()
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
