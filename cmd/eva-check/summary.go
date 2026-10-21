package main

type summary struct {
	Searches           int     `json:"searches"`
	FirstPassSuccesses int     `json:"firstPassSuccesses"`
	RecoveredSuccesses int     `json:"recoveredSuccesses"`
	FinalFailures      int     `json:"finalFailures"`
	PartialResults     int     `json:"partialResults"`
	EmptySuccesses     int     `json:"emptySuccesses"`
	RecoveryEvents     int     `json:"recoveryEvents"`
	UserSuccessPercent float64 `json:"userSuccessPercent"`
}

func summarize(results []measurement) summary {
	s := summary{Searches: len(results)}
	for _, r := range results {
		s.RecoveryEvents += len(r.Recoveries)
		if r.Status == "completed" && r.Complete {
			if len(r.Recoveries) == 0 {
				s.FirstPassSuccesses++
			} else {
				s.RecoveredSuccesses++
			}
			if r.Itineraries == 0 {
				s.EmptySuccesses++
			}
		} else if r.Status == "completed" {
			s.PartialResults++
		} else {
			s.FinalFailures++
		}
	}
	if s.Searches > 0 {
		s.UserSuccessPercent = 100 * float64(s.FirstPassSuccesses+s.RecoveredSuccesses) / float64(s.Searches)
	}
	return s
}
