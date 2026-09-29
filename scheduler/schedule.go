package scheduler

// reservation is the shadow-time reservation held for the queue head.
type reservation struct {
	headID string
	// shadow is the earliest time at which the head could start given the
	// running set observed during the scheduling pass.
	shadow Tick
	// spare is the node count still free at shadow after the head is placed:
	// spare = N - head.Nodes - (nodes of jobs still running at shadow).
	spare int
	// needRelease counts running jobs, in canonical release order, that must
	// end before the head fits. All of them satisfy end <= shadow.
	needRelease []string
}

// scheduleLocked runs one scheduling pass at the current time:
//
//  1. Start queue-head jobs in submission order while they fit immediately.
//  2. When the head does not fit, reserve its earliest feasible start
//     ("shadow time") by releasing running jobs one by one in estimated-end
//     order (ties by job id), and derive the surplus ("spare") node count
//     still available at that time after serving the head.
//  3. Scan the remaining queued jobs in submission order. A job backfills
//     iff it fits now and either (a) it finishes by the shadow time, or
//     (b) its nodes fit within the spare budget, which they then consume.
//
// Started jobs are appended to StartEvent output in start order, which makes
// the scheduling result fully reproducible under replayed inputs.
func (s *Scheduler) scheduleLocked() []StartEvent {
	var events []StartEvent

	// Phase 1: immediate starts of queue heads, in submission order.
	for len(s.queue) > 0 {
		head := s.queue[0]
		used := s.usedNodesLocked()
		if used+head.Nodes > s.n {
			s.logger.Printf("decision head-blocked job=%q needs=%d used=%d n=%d",
				head.ID, head.Nodes, used, s.n)
			break
		}
		s.startHeadLocked(head, StartImmediate)
		events = append(events, StartEvent{Time: s.now, JobID: head.ID, Reason: StartImmediate})
	}
	if len(s.queue) == 0 {
		return events
	}

	// Phase 2: reservation for the blocked head.
	res := s.computeReservationLocked(s.queue[0])
	s.logger.Printf("decision reservation head=%q shadow=%d spare=%d release-order=%v",
		res.headID, res.shadow, res.spare, res.needRelease)

	// Phase 3: backfill scan over the jobs behind the head, preserving
	// submission order. The scan walks the live queue with a cursor:
	// starting a job deletes it (the next job shifts into the same index, so
	// the cursor does not advance); a skipped job leaves the cursor behind
	// the head and we advance past it. The head always stays at index 0.
	i := 1
	for i < len(s.queue) {
		job := s.queue[i]
		used := s.usedNodesLocked()
		if used+job.Nodes > s.n {
			s.logger.Printf("decision backfill-skip job=%q needs=%d used=%d n=%d (does not fit now)",
				job.ID, job.Nodes, used, s.n)
			i++
			continue
		}

		endsByShadow := s.now+job.Duration <= res.shadow
		withinSpare := job.Nodes <= res.spare

		switch {
		case endsByShadow:
			// Condition (a): frees every node it occupies by the shadow
			// time, so the reservation cannot be delayed.
			s.startBackfillLocked(job, StartBackfillTime)
			events = append(events, StartEvent{Time: s.now, JobID: job.ID, Reason: StartBackfillTime})
			s.logger.Printf("decision backfill-time job=%q end=%d <= shadow=%d",
				job.ID, s.now+job.Duration, res.shadow)
		case withinSpare:
			// Condition (b): the nodes survive past the shadow time but are
			// part of the surplus left after the head is placed.
			res.spare -= job.Nodes
			s.startBackfillLocked(job, StartBackfillSpare)
			events = append(events, StartEvent{Time: s.now, JobID: job.ID, Reason: StartBackfillSpare})
			s.logger.Printf("decision backfill-spare job=%q needs=%d spare-left=%d",
				job.ID, job.Nodes, res.spare)
		default:
			s.logger.Printf("decision backfill-skip job=%q end=%d > shadow=%d and needs=%d > spare=%d",
				job.ID, s.now+job.Duration, res.shadow, job.Nodes, res.spare)
			i++
			continue
		}
	}
	return events
}

// computeReservationLocked determines the head's shadow time and the spare
// node count available at that time after the head is served.
func (s *Scheduler) computeReservationLocked(head Job) reservation {
	running := make([]*runningJob, 0, len(s.running))
	for _, rj := range s.running {
		running = append(running, rj)
	}
	sortRunningByEnd(running)

	used := s.usedNodesLocked()
	released := 0
	var releaseIDs []string

	// Release running jobs in canonical order until the head fits. At the
	// stopping point the released set is a prefix of the end-ordered list,
	// so every released job ends no later than the shadow time.
	for _, rj := range running {
		if used-released+head.Nodes <= s.n {
			break
		}
		released += rj.job.Nodes
		releaseIDs = append(releaseIDs, rj.job.ID)
	}

	var shadow Tick
	if len(releaseIDs) == 0 {
		// Defensive: a reservation is only computed for a blocked head, so
		// at least one job must be released. Fall back to "now".
		shadow = s.now
	} else {
		shadow = s.running[releaseIDs[len(releaseIDs)-1]].end
	}

	// Nodes still occupied at the shadow time: every running job whose end
	// is strictly after shadow, plus the head itself. Spare is what remains.
	occupiedAtShadow := head.Nodes
	for _, rj := range running {
		if rj.end > shadow {
			occupiedAtShadow += rj.job.Nodes
		}
	}
	spare := s.n - occupiedAtShadow
	if spare < 0 {
		spare = 0
	}

	return reservation{
		headID:      head.ID,
		shadow:      shadow,
		spare:       spare,
		needRelease: releaseIDs,
	}
}

func (s *Scheduler) startHeadLocked(job Job, reason StartReason) {
	s.startJobLocked(job)
	s.queue = s.queue[1:]
	s.logger.Printf("decision start job=%q at=%d reason=%s nodes=%d end=%d",
		job.ID, s.now, reason, job.Nodes, s.now+job.Duration)
}

func (s *Scheduler) startBackfillLocked(job Job, reason StartReason) {
	s.startJobLocked(job)
	s.removeQueuedLocked(job.ID)
	s.logger.Printf("decision start job=%q at=%d reason=%s nodes=%d end=%d",
		job.ID, s.now, reason, job.Nodes, s.now+job.Duration)
}

func (s *Scheduler) startJobLocked(job Job) {
	s.running[job.ID] = &runningJob{
		job:   job,
		start: s.now,
		end:   s.now + job.Duration,
	}
}

func (s *Scheduler) removeQueuedLocked(id string) {
	for i, job := range s.queue {
		if job.ID == id {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			return
		}
	}
}
