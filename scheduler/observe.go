package scheduler

// RunningInfo is observable state of one running job, used by tests and
// diagnostics that need estimated end times without reaching into internals.
type RunningInfo struct {
	Nodes int
	Start Tick
	End   Tick
}

// RunningInfo returns the observable runtime info for a running job.
func (s *Scheduler) RunningInfo(id string) (RunningInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rj, ok := s.running[id]
	if !ok {
		return RunningInfo{}, false
	}
	return RunningInfo{Nodes: rj.job.Nodes, Start: rj.start, End: rj.end}, true
}

// QueuedJob returns the declaration of a still-queued job.
func (s *Scheduler) QueuedJob(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range s.queue {
		if job.ID == id {
			return job, true
		}
	}
	return Job{}, false
}

// N returns the cluster node count.
func (s *Scheduler) N() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

func (s *Scheduler) nSafe() int {
	return s.N()
}
