package scheduler

import (
	"log"
	"os"
	"sync"
)

// Tick is the scheduler's unit of injected time. The scheduler never reads a
// wall clock itself; time only moves through Scheduler.Advance.
type Tick = int64

// Job is a job declaration submitted to the scheduler.
type Job struct {
	// ID is the unique job identifier; tie-breaks are ordered by it.
	ID string
	// Nodes is the number of (identical) nodes the job needs.
	Nodes int
	// Duration is the estimated runtime; the job is force-finished once it
	// has run for this many ticks from its actual start.
	Duration Tick
}

// StartReason explains why a queued job was started during a scheduling pass.
type StartReason string

const (
	// StartImmediate means the job fit without disturbing any reservation.
	StartImmediate StartReason = "immediate"
	// StartReservation means the job is the queue head whose reservation
	// became due.
	StartReservation StartReason = "reservation"
	// StartBackfillTime means the job backfills because it finishes no later
	// than the head's shadow time.
	StartBackfillTime StartReason = "backfill:ends-by-shadow"
	// StartBackfillSpare means the job backfills using surplus nodes that
	// remain available at the shadow time.
	StartBackfillSpare StartReason = "backfill:within-spare"
)

// StartEvent records one job start produced by a scheduling pass.
type StartEvent struct {
	Time   Tick
	JobID  string
	Reason StartReason
}

// FinishEvent records one job termination.
type FinishEvent struct {
	Time   Tick
	JobID  string
	Forced bool
}

type runningJob struct {
	job   Job
	start Tick
	end   Tick
}

// Scheduler is a reservation-with-backfill scheduler for N identical nodes.
// All exported operations are safe for concurrent use. Rejected operations
// never mutate the queue or the running set.
type Scheduler struct {
	mu      sync.Mutex
	n       int
	now     Tick
	queue   []Job
	running map[string]*runningJob
	known   map[string]struct{}
	logger  *log.Logger
}

// New creates a scheduler for n identical nodes.
// If logger is nil a default logger writing to stderr is used.
func New(n int, logger *log.Logger) (*Scheduler, error) {
	if n <= 0 {
		return nil, ErrInvalidNodes
	}
	if logger == nil {
		logger = log.New(os.Stderr, "[scheduler] ", log.LstdFlags|log.Lmicroseconds)
	}
	return &Scheduler{
		n:       n,
		running: make(map[string]*runningJob),
		known:   make(map[string]struct{}),
		logger:  logger,
	}, nil
}

// Submit appends a job to the queue in submission order and schedules.
// A rejected submission leaves both the queue and the running set untouched.
func (s *Scheduler) Submit(job Job) (starts []StartEvent, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Printf("input Submit job={id:%q nodes:%d duration:%d} now=%d",
		job.ID, job.Nodes, job.Duration, s.now)
	defer func() {
		if err != nil {
			s.logger.Printf("output Submit job=%q rejected reason=%v", job.ID, err)
			return
		}
		s.logger.Printf("output Submit job=%q accepted starts=%v", job.ID, starts)
	}()

	if job.Nodes <= 0 || job.Nodes > s.n {
		return nil, ErrInvalidJobNodes
	}
	if job.Duration <= 0 {
		return nil, ErrInvalidDuration
	}
	if _, dup := s.known[job.ID]; dup {
		return nil, ErrDuplicateID
	}

	s.known[job.ID] = struct{}{}
	s.queue = append(s.queue, job)
	starts = s.scheduleLocked()
	return starts, nil
}

// Finish ends a running job early at the current time and schedules.
// Finishing a job that is not running is rejected without side effects.
func (s *Scheduler) Finish(jobID string) (finishes []FinishEvent, starts []StartEvent, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Printf("input Finish job=%q now=%d", jobID, s.now)
	defer func() {
		if err != nil {
			s.logger.Printf("output Finish job=%q rejected reason=%v", jobID, err)
			return
		}
		s.logger.Printf("output Finish job=%q finishes=%v starts=%v", jobID, finishes, starts)
	}()

	if _, ok := s.running[jobID]; !ok {
		return nil, nil, ErrNotRunning
	}
	finishes = []FinishEvent{{Time: s.now, JobID: jobID, Forced: false}}
	s.removeRunningLocked(jobID)
	s.logger.Printf("decision early-finish job=%q at=%d; reschedule", jobID, s.now)
	starts = s.scheduleLocked()
	return finishes, starts, nil
}

// Advance moves the injected clock to t, force-finishing jobs whose estimated
// end time is reached, then schedules.
// Advancing to an earlier time is rejected without side effects.
func (s *Scheduler) Advance(t Tick) (finishes []FinishEvent, starts []StartEvent, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Printf("input Advance from=%d to=%d", s.now, t)
	defer func() {
		if err != nil {
			s.logger.Printf("output Advance to=%d rejected reason=%v", t, err)
			return
		}
		s.logger.Printf("output Advance now=%d finishes=%v starts=%v", t, finishes, starts)
	}()

	if t < s.now {
		return nil, nil, ErrClockRollback
	}
	s.now = t

	// Force-finish, deterministically, every job whose estimated end is due.
	due := make([]*runningJob, 0)
	for _, rj := range s.running {
		if rj.end <= t {
			due = append(due, rj)
		}
	}
	sortRunningByEnd(due)
	if len(due) > 0 {
		finishes = make([]FinishEvent, 0, len(due))
		for _, rj := range due {
			finishes = append(finishes, FinishEvent{
				Time:   rj.end,
				JobID:  rj.job.ID,
				Forced: true,
			})
			s.removeRunningLocked(rj.job.ID)
			s.logger.Printf("decision force-finish job=%q at=%d", rj.job.ID, rj.end)
		}
	}
	starts = s.scheduleLocked()
	return finishes, starts, nil
}

// Snapshot is a point-in-time view returned by Query.
type Snapshot struct {
	Time       Tick
	UsedNodes  int
	QueuedIDs  []string
	RunningIDs []string
}

// Query returns the current queue and running set.
func (s *Scheduler) Query() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := Snapshot{Time: s.now, UsedNodes: 0,
		QueuedIDs:  make([]string, 0, len(s.queue)),
		RunningIDs: make([]string, 0, len(s.running))}
	for _, job := range s.queue {
		snap.QueuedIDs = append(snap.QueuedIDs, job.ID)
	}
	for _, rj := range s.running {
		snap.RunningIDs = append(snap.RunningIDs, rj.job.ID)
		snap.UsedNodes += rj.job.Nodes
	}
	sortStrings(snap.RunningIDs)

	s.logger.Printf("input/output Query now=%d used=%d queued=%v running=%v",
		snap.Time, snap.UsedNodes, snap.QueuedIDs, snap.RunningIDs)
	return snap
}

func (s *Scheduler) removeRunningLocked(jobID string) {
	if rj, ok := s.running[jobID]; ok {
		s.logger.Printf("internal remove-running job=%q nodes=%d", jobID, rj.job.Nodes)
		delete(s.running, jobID)
	}
}

// usedNodesLocked returns the sum of nodes held by running jobs.
func (s *Scheduler) usedNodesLocked() int {
	used := 0
	for _, rj := range s.running {
		used += rj.job.Nodes
	}
	return used
}
