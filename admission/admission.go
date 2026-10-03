package admission

import (
	"errors"
	"math"
	"sort"
	"sync"
)

var (
	ErrInvalidArguments = errors.New("invalid arguments")
	ErrClockRolledBack  = errors.New("clock rolled back")
	ErrDuplicateID      = errors.New("duplicate job id")
	ErrImpossible       = errors.New("job cannot finish even on a dedicated processor")
	ErrOverloaded       = errors.New("overloaded")
)

type PendingJob struct {
	ID        string
	Remaining int64
	Started   bool
}

type SubmitResult struct {
	Accepted bool
	Evicted  []string
	Reason   error
}

type Controller struct {
	mu          sync.RWMutex
	clock       int64
	jobs        []*job
	totalValue  int64
	evicted     []string
	sortCount   int
	feasibility int
}

type job struct {
	id        string
	remaining int64
	deadline  int64
	tolerance int64
	value     int64
	started   bool
}

func New() *Controller {
	return &Controller{
		jobs:    make([]*job, 0),
		evicted: make([]string, 0),
	}
}

func (c *Controller) Submit(id string, now, execution, deadline, tolerance, value int64) SubmitResult {
	if !validSubmit(id, now, execution, deadline, tolerance, value) {
		return SubmitResult{Reason: ErrInvalidArguments}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.sortCount = 0
	c.feasibility = 0

	if now < c.clock {
		return SubmitResult{Reason: ErrClockRolledBack}
	}

	working := cloneJobs(c.jobs)
	settled, remaining, advancedClock := advanceJobs(working, c.clock, now)
	working = remaining

	if hasJobID(working, id) {
		return SubmitResult{Reason: ErrDuplicateID}
	}
	if now+execution > deadline+tolerance {
		return SubmitResult{Reason: ErrImpossible}
	}

	working = append(working, &job{
		id:        id,
		remaining: execution,
		deadline:  deadline,
		tolerance: tolerance,
		value:     value,
	})
	c.sortCount++
	sort.Slice(working, func(i, j int) bool {
		return edfLess(working[i], working[j])
	})

	evictedNow := make([]string, 0)
	c.feasibility++
	if !isFeasible(working, advancedClock) {
		for {
			index := leastDensityUnstarted(working)
			if index < 0 {
				return SubmitResult{Reason: ErrOverloaded}
			}

			removed := working[index]
			working = append(working[:index], working[index+1:]...)

			if removed.id == id {
				return SubmitResult{Reason: ErrOverloaded}
			}
			evictedNow = append(evictedNow, removed.id)

			c.feasibility++
			if isFeasible(working, advancedClock) {
				break
			}
		}
	}

	c.jobs = working
	c.clock = advancedClock
	c.totalValue = addSaturating(c.totalValue, settled)
	c.evicted = append(c.evicted, evictedNow...)

	return SubmitResult{Accepted: true, Evicted: append([]string(nil), evictedNow...)}
}

func (c *Controller) Advance(now int64) error {
	if now < 0 || now > maxTime {
		return ErrInvalidArguments
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.clock {
		return ErrClockRolledBack
	}

	settled, remaining, advancedClock := advanceJobs(c.jobs, c.clock, now)
	c.jobs = remaining
	c.clock = advancedClock
	c.totalValue = addSaturating(c.totalValue, settled)
	return nil
}

func (c *Controller) Pending() []PendingJob {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]PendingJob, 0, len(c.jobs))
	for _, pending := range c.jobs {
		result = append(result, PendingJob{
			ID:        pending.id,
			Remaining: pending.remaining,
			Started:   pending.started,
		})
	}
	return result
}

func (c *Controller) Value() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.totalValue
}

func (c *Controller) Evicted() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]string(nil), c.evicted...)
}

const (
	maxTime      = int64(1_000_000_000_000_000)
	maxWork      = int64(1_000_000)
	maxValueSize = int64(1_000_000)
)

func validSubmit(id string, now, execution, deadline, tolerance, value int64) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	if now < 0 || now > maxTime {
		return false
	}
	if execution < 1 || execution > maxWork {
		return false
	}
	if deadline < 0 || deadline > maxTime {
		return false
	}
	if tolerance < 0 || tolerance > maxWork {
		return false
	}
	return value >= 1 && value <= maxValueSize
}

func cloneJobs(jobs []*job) []*job {
	cloned := make([]*job, len(jobs))
	for i, pending := range jobs {
		copyJob := *pending
		cloned[i] = &copyJob
	}
	return cloned
}

func advanceJobs(jobs []*job, clock, now int64) (int64, []*job, int64) {
	var settled int64
	current := clock

	for len(jobs) > 0 {
		available := now - current
		if available <= 0 {
			break
		}

		next := jobs[0]
		if next.remaining > available {
			next.remaining -= available
			next.started = true
			break
		}

		current += next.remaining
		settled = addSaturating(settled, completionValue(next, current))
		jobs = jobs[1:]
	}

	return settled, jobs, now
}

func completionValue(pending *job, finish int64) int64 {
	late := finish - pending.deadline
	if late < 0 {
		late = 0
	}
	denominator := pending.tolerance + 1
	factor := denominator - late
	return pending.value * factor / denominator
}

func hasJobID(jobs []*job, id string) bool {
	for _, pending := range jobs {
		if pending.id == id {
			return true
		}
	}
	return false
}

func edfLess(left, right *job) bool {
	if left.deadline != right.deadline {
		return left.deadline < right.deadline
	}
	return left.id < right.id
}

func isFeasible(jobs []*job, start int64) bool {
	current := start
	feasible := true
	for _, pending := range jobs {
		if feasible {
			if current > math.MaxInt64-pending.remaining {
				feasible = false
			} else {
				current += pending.remaining
				if current > pending.deadline+pending.tolerance {
					feasible = false
				}
			}
		}
	}
	return feasible
}

func leastDensityUnstarted(jobs []*job) int {
	best := -1
	for i, pending := range jobs {
		if pending.started {
			continue
		}
		if best < 0 || lowerDensity(pending, jobs[best]) {
			best = i
		}
	}
	return best
}

func lowerDensity(candidate, best *job) bool {
	left := candidate.value * best.remaining
	right := best.value * candidate.remaining
	if left != right {
		return left < right
	}
	return candidate.id > best.id
}

func addSaturating(left, right int64) int64 {
	if left > math.MaxInt64-right {
		return math.MaxInt64
	}
	return left + right
}
