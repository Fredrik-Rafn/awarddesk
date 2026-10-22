package jobs

import "awarddesk/internal/award"

func cloneJob(job *Job) Job {
	out := *job
	out.queueTimer, out.runTimer = nil, nil
	out.Timeline = append([]StageEvent(nil), job.Timeline...)
	for i, event := range out.Timeline {
		if event.Recovery != nil {
			recovery := *event.Recovery
			out.Timeline[i].Recovery = &recovery
		}
	}
	out.Recoveries = append([]award.Recovery(nil), job.Recoveries...)
	if job.Result != nil {
		result := award.CloneResult(*job.Result)
		out.Result = &result
	}
	if job.Error != nil {
		failure := *job.Error
		out.Error = &failure
	}
	if job.Progress.Recovery != nil {
		recovery := *job.Progress.Recovery
		out.Progress.Recovery = &recovery
	}
	if job.StartedAt != nil {
		started := *job.StartedAt
		out.StartedAt = &started
	}
	if job.FinishedAt != nil {
		finished := *job.FinishedAt
		out.FinishedAt = &finished
	}
	return out
}
