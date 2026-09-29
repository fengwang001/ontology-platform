package scheduler

import "sort"

// sortRunningByEnd orders running jobs by estimated end time, breaking ties by
// job identifier. This ordering is the canonical shadow-time release order.
func sortRunningByEnd(jobs []*runningJob) {
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].end != jobs[j].end {
			return jobs[i].end < jobs[j].end
		}
		return jobs[i].job.ID < jobs[j].job.ID
	})
}

func sortStrings(xs []string) {
	sort.Strings(xs)
}
