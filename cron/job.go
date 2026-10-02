package cron

import "sync"

// Policy decides what happens when a missed firing is caught up
// while tasks from earlier firings are still active.
type Policy int

const (
	// Allow always starts a new task, even when tasks are active.
	Allow Policy = iota
	// Forbid skips the catch-up while any task is active.
	Forbid
	// Replace terminates every active task before starting the new one.
	Replace
)

// maxDeadline is the largest accepted D (deadline window in minutes).
const maxDeadline = 1_000_000_000

// Job is a CronJob-style controller that replays missed firings at
// the latest one only. All methods are safe for concurrent use; their
// effects are equivalent to some serial execution.
type Job struct {
	mu sync.Mutex

	spec     *Spec
	d        int
	policy   Policy
	l        int // last scheduled firing time
	lastSync int // watermark of the last successful Sync
	active   map[int]struct{}
	nextID   int
	suspend  bool

	skipped  int
	replaced int

	// nfCalls counts NextFire invocations during the most recent Sync;
	// unexported instrumentation proving the 102-call bound.
	nfCalls int
}

// NewJob constructs a Job created at minute created. D is -1 for no
// deadline or a non-negative window length of at most 1e9 minutes.
func NewJob(spec string, created, d int, policy Policy) (*Job, error) {
	parsed, err := Parse(spec)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	if created < 0 || created > maxMinute {
		return nil, ErrInvalidArgument
	}
	if d < -1 || d > maxDeadline {
		return nil, ErrInvalidArgument
	}
	if policy != Allow && policy != Forbid && policy != Replace {
		return nil, ErrInvalidArgument
	}
	return &Job{
		spec:     parsed,
		d:        d,
		policy:   policy,
		l:        created,
		lastSync: created,
		active:   map[int]struct{}{},
	}, nil
}

// SetSuspend suspends or resumes the job. Resuming performs no
// catch-up; missed firings are only judged by later Sync calls.
func (j *Job) SetSuspend(b bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.suspend = b
}

// Active returns the ids of currently active tasks in ascending order.
func (j *Job) Active() []int {
	j.mu.Lock()
	defer j.mu.Unlock()
	ids := make([]int, 0, len(j.active))
	for id := range j.active {
		ids = append(ids, id)
	}
	sortInts(ids)
	return ids
}

// Skipped returns the number of firings skipped under Forbid.
func (j *Job) Skipped() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.skipped
}

// Replaced returns the total number of active tasks terminated by
// Replace catch-ups.
func (j *Job) Replaced() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.replaced
}

// LastScheduled returns L, the latest scheduled firing time.
func (j *Job) LastScheduled() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.l
}

// SyncResult reports what one Sync call did.
type SyncResult struct {
	// Fired is true when a new task was started.
	Fired bool
	// TaskID is the new task id when Fired is true.
	TaskID int
	// FireTime is the caught-up firing time when a firing was handled.
	FireTime int
	// Skipped is true when Forbid suppressed the catch-up.
	Skipped bool
}

// Sync examines firings in (L, now] (and, when D >= 0, in
// [now-D, now]) and replays at most the latest one.
//
// Errors are reported in the order: invalid time, clock backwards,
// too many missed firings. A rejected call changes no state,
// including the Sync watermark.
func (j *Job) Sync(now int) (SyncResult, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if now < 0 || now > maxMinute {
		return SyncResult{}, ErrInvalidTime
	}
	if now < j.lastSync {
		return SyncResult{}, ErrClockBackwards
	}

	// Even while suspended the watermark advances; L does not.
	if j.suspend {
		j.lastSync = now
		return SyncResult{}, nil
	}

	lower := 0
	if j.d >= 0 {
		lower = now - j.d
		if lower < 0 {
			lower = 0
		}
	}

	// Collect window firings. At most 102 NextFire calls are made:
	// once the window exceeds 100 firings the count can stop.
	j.nfCalls = 0
	var window []int
	t := j.l
	for len(window) <= 100 {
		j.nfCalls++
		next, err := NextFire(j.spec, t)
		if err != nil {
			break
		}
		if next > now {
			break
		}
		t = next
		if next >= lower {
			window = append(window, next)
		}
	}
	if len(window) > 100 {
		return SyncResult{}, ErrTooManyMissed
	}

	j.lastSync = now
	if len(window) == 0 {
		return SyncResult{}, nil
	}

	latest := window[len(window)-1]
	j.l = latest
	switch j.policy {
	case Forbid:
		if len(j.active) > 0 {
			j.skipped++
			return SyncResult{FireTime: latest, Skipped: true}, nil
		}
	case Replace:
		j.replaced += len(j.active)
		j.active = map[int]struct{}{}
	}
	j.nextID++
	id := j.nextID
	j.active[id] = struct{}{}
	return SyncResult{Fired: true, TaskID: id, FireTime: latest}, nil
}

// Finish removes task id from the active set.
func (j *Job) Finish(id, now int) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if now < 0 || now > maxMinute {
		return ErrInvalidTime
	}
	if _, ok := j.active[id]; !ok {
		return ErrTaskNotFound
	}
	delete(j.active, id)
	return nil
}

// sortInts sorts a small slice of ints in place.
func sortInts(xs []int) {
	for i := 1; i < len(xs); i++ {
		for k := i; k > 0 && xs[k-1] > xs[k]; k-- {
			xs[k-1], xs[k] = xs[k], xs[k-1]
		}
	}
}
