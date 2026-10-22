package fleet

import (
	"crypto/sha256"
	"encoding/binary"
	"strconv"
	"time"
)

const MaxPreparationFailures = 15

// preparationRetryDelay spreads retries across a 15–30 minute window. It is
// stable for one account/attempt, so restarts do not shorten its cooldown.
func preparationRetryDelay(id string, failures int) time.Duration {
	hash := sha256.Sum256([]byte(id + ":" + strconv.Itoa(failures)))
	spread := binary.BigEndian.Uint64(hash[:8]) % uint64(15*time.Minute+1)
	return 15*time.Minute + time.Duration(spread)
}

// recoverPreparationStatus upgrades old permanent pauses and records interrupted
// work as a delayed fresh attempt. No credential request is replayed on restart.
func recoverPreparationStatus(st Status, now time.Time) Status {
	if st.NeedsAttention && st.ConsecutiveFailures == 0 {
		st.ConsecutiveFailures = 1
		st.NextAttempt = st.CheckedAt.Add(preparationRetryDelay(st.ID, 1))
		st.NeedsAttention = false
	}
	if st.State == "preparing" || st.State == "searching" {
		st.State = "failed"
		st.Message = "Previous operation was interrupted; scheduled a fresh session check"
		st.ConsecutiveFailures++
		st.NextAttempt = now.Add(preparationRetryDelay(st.ID, st.ConsecutiveFailures))
	}
	if st.ConsecutiveFailures >= MaxPreparationFailures {
		st.NeedsAttention = true
		st.NextAttempt = time.Time{}
	}
	return st
}
