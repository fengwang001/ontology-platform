// Package sjob holds a single scheduled job: readiness, (length,
// registration-order) comparison and non-preemptive execution. It depends on
// no other package of this module.
package sjob

// Job is one unit of work submitted at tick arrive and requiring length
// consecutive CPU ticks. reg is its global registration order.
type Job struct {
	id     string
	arrive int64
	length int64
	reg    int

	started   bool
	start     int64
	remaining int64
	done      bool
	finish    int64
}

// New constructs a job with registration order reg.
func New(id string, arrive, length int64, reg int) *Job {
	return &Job{id: id, arrive: arrive, length: length, reg: reg, remaining: length}
}

func (j *Job) ID() string        { return j.id }
func (j *Job) Arrive() int64     { return j.arrive }
func (j *Job) Length() int64     { return j.length }
func (j *Job) Reg() int          { return j.reg }
func (j *Job) Started() bool     { return j.started }
func (j *Job) Done() bool        { return j.done }
func (j *Job) StartTick() int64  { return j.start }
func (j *Job) FinishTick() int64 { return j.finish }

// ReadyAt reports whether the job has entered the ready set at tick t.
func (j *Job) ReadyAt(t int64) bool { return t >= j.arrive }

// Less implements the (length, registration order) ordering: shorter jobs
// come first; equal length is broken by earlier registration.
func (j *Job) Less(o *Job) bool {
	if j.length != o.length {
		return j.length < o.length
	}
	return j.reg < o.reg
}

// Begin starts the job non-preemptively at tick t and runs it straight to
// completion, returning the finish tick. Once begun the whole length is
// consumed in one indivisible step; nothing can interrupt it.
func (j *Job) Begin(t int64) int64 {
	j.started = true
	j.start = t
	j.remaining = 0
	j.done = true
	j.finish = t + j.length
	return j.finish
}

// Wait returns the number of ticks spent between readiness (arrive) and the
// tick execution began. It returns -1 while the job has never started.
func (j *Job) Wait() int64 {
	if !j.started {
		return -1
	}
	return j.start - j.arrive
}
