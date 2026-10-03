// Package mcsched implements a runtime mode monitor for mixed-criticality
// task sets scheduled on a single core under fixed-priority preemptive
// scheduling.
//
// The system has two modes, LO and HI, and starts in LO. Each tick t is
// processed in three steps, in order:
//
//  1. Every unfinished job with deadline d == t is marked missed (counted
//     per criticality) and removed.
//  2. If the mode is HI and no unfinished job remains, the mode recovers
//     to LO.
//  3. Jobs with release time t (t >= phi and (t-phi) divisible by T) are
//     released. In HI mode, releases of LO tasks are dropped (skipped
//     counter incremented, the job index is still consumed); otherwise the
//     job gets remaining = its actual demand, executed = 0, d = t+T.
//
// Then the unfinished job with the smallest prio runs for one tick:
// executed+1, remaining-1. If remaining reaches zero the job completes and
// is removed. Otherwise, if the mode is LO, the job belongs to a HI task
// and executed equals its CL, the system switches to HI immediately and
// all unfinished LO jobs are discarded.
package mcsched

import (
	"errors"
	"sync"
)

// Criticality is the criticality level of a task.
type Criticality int

const (
	LO Criticality = iota
	HI
)

// Mode is the system mode.
type Mode int

const (
	ModeLO Mode = iota
	ModeHI
)

// Rejection reasons. Returned errors wrap exactly one of these sentinels,
// so callers can distinguish causes with errors.Is.
var (
	ErrStarted       = errors.New("mcsched: monitor already started")
	ErrInvalidParam  = errors.New("mcsched: invalid parameter")
	ErrDuplicateID   = errors.New("mcsched: duplicate task id")
	ErrDuplicatePrio = errors.New("mcsched: duplicate priority")
	ErrTooManyTasks  = errors.New("mcsched: task limit reached")
	ErrTaskNotFound  = errors.New("mcsched: task not found")
	ErrJobReleased   = errors.New("mcsched: job already released")
)

const (
	maxTasks   = 16
	maxIDBytes = 32
	maxPeriod  = 1000
	maxPhi     = 1_000_000
	maxStep    = 1_000_000
)

// TaskSpec describes a task to add.
//
//	ID   non-empty, at most 32 bytes
//	Crit LO or HI
//	CL   low budget, >= 1
//	CH   high budget; CH == CL for LO tasks, CL <= CH for HI tasks
//	T    period (relative deadline equals T), 1..1000, CH <= T
//	Prio priority, positive, smaller wins, globally unique
//	Phi  first release time, 0..1e6
type TaskSpec struct {
	ID   string
	Crit Criticality
	CL   int
	CH   int
	T    int
	Prio int
	Phi  int
}

// Stats holds the cumulative counters of a Monitor.
type Stats struct {
	Switches  int // LO -> HI mode switches
	Restores  int // HI -> LO mode recoveries
	Discarded int // unfinished LO jobs discarded on a switch
	Skipped   int // LO releases dropped while in HI mode
	MissedLO  int // LO jobs that reached their deadline unfinished
	MissedHI  int // HI jobs that reached their deadline unfinished
	Completed int // jobs that completed
}

type task struct {
	spec    TaskSpec
	demands map[int]int // job index -> actual execution demand
}

type job struct {
	tk   *task
	k    int
	rem  int
	exec int
	d    int
}

// Monitor is a runtime mode monitor. All methods are safe for concurrent
// use; the result is equivalent to some serial order of the calls.
type Monitor struct {
	mu       sync.Mutex
	tasks    map[string]*task
	taskList []*task // in AddTask order, for deterministic release scans
	prios    map[int]bool
	jobs     []*job
	mode     Mode
	now      int
	runLog   []string // runLog[t] is the task id run at tick t, "" if idle
	stats    Stats
}

// NewMonitor returns an empty Monitor in LO mode at time 0.
func NewMonitor() *Monitor {
	return &Monitor{
		tasks: make(map[string]*task),
		prios: make(map[int]bool),
	}
}

// AddTask registers a task. It fails, reporting only the first reason, if
// the monitor has already executed a tick (ErrStarted), the parameters are
// illegal (ErrInvalidParam), the id is duplicated (ErrDuplicateID), the
// priority is duplicated (ErrDuplicatePrio), or 16 tasks already exist
// (ErrTooManyTasks). A rejected call changes nothing.
func (m *Monitor) AddTask(spec TaskSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addTask(spec)
}

func (m *Monitor) addTask(spec TaskSpec) error {
	if m.now > 0 {
		return ErrStarted
	}
	if err := validateSpec(spec); err != nil {
		return err
	}
	if _, ok := m.tasks[spec.ID]; ok {
		return ErrDuplicateID
	}
	if m.prios[spec.Prio] {
		return ErrDuplicatePrio
	}
	if len(m.taskList) >= maxTasks {
		return ErrTooManyTasks
	}
	tk := &task{spec: spec, demands: make(map[int]int)}
	m.tasks[spec.ID] = tk
	m.taskList = append(m.taskList, tk)
	m.prios[spec.Prio] = true
	return nil
}

func validateSpec(spec TaskSpec) error {
	if len(spec.ID) == 0 || len(spec.ID) > maxIDBytes {
		return ErrInvalidParam
	}
	if spec.Crit != LO && spec.Crit != HI {
		return ErrInvalidParam
	}
	if spec.CL < 1 {
		return ErrInvalidParam
	}
	if spec.Crit == LO && spec.CH != spec.CL {
		return ErrInvalidParam
	}
	if spec.Crit == HI && spec.CH < spec.CL {
		return ErrInvalidParam
	}
	if spec.T < 1 || spec.T > maxPeriod || spec.CH > spec.T {
		return ErrInvalidParam
	}
	if spec.Prio < 1 {
		return ErrInvalidParam
	}
	if spec.Phi < 0 || spec.Phi > maxPhi {
		return ErrInvalidParam
	}
	return nil
}

// SetDemand sets the actual execution demand of the k-th job (released at
// phi+k*T, k from 0) of the named task to c. It fails, reporting only the
// first reason, if the id does not exist (ErrTaskNotFound), k is negative
// or c is out of range (ErrInvalidParam: 1..CL for LO, 1..CH for HI), or
// the job's release time is before the current time (ErrJobReleased).
// Repeating SetDemand for the same job keeps the last value. A rejected
// call changes nothing.
func (m *Monitor) SetDemand(id string, k, c int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.setDemand(id, k, c)
}

func (m *Monitor) setDemand(id string, k, c int) error {
	tk, ok := m.tasks[id]
	if !ok {
		return ErrTaskNotFound
	}
	hi := tk.spec.CL
	if tk.spec.Crit == HI {
		hi = tk.spec.CH
	}
	if k < 0 || c < 1 || c > hi {
		return ErrInvalidParam
	}
	release := int64(tk.spec.Phi) + int64(k)*int64(tk.spec.T)
	if release < int64(m.now) {
		return ErrJobReleased
	}
	tk.demands[k] = c
	return nil
}

// Step advances the monitor by n ticks, 1 <= n <= 1e6. An invalid n
// returns ErrInvalidParam and changes nothing.
func (m *Monitor) Step(n int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.step(n)
}

func (m *Monitor) step(n int) error {
	if n < 1 || n > maxStep {
		return ErrInvalidParam
	}
	for i := 0; i < n; i++ {
		m.tick()
	}
	return nil
}

// tick processes a single tick at the current time.
func (m *Monitor) tick() {
	t := m.now

	// Step 1: jobs with d == t are missed and removed.
	kept := m.jobs[:0]
	for _, j := range m.jobs {
		if j.d == t {
			if j.tk.spec.Crit == HI {
				m.stats.MissedHI++
			} else {
				m.stats.MissedLO++
			}
			continue
		}
		kept = append(kept, j)
	}
	m.jobs = kept

	// Step 2: recover to LO when HI and completely idle.
	if m.mode == ModeHI && len(m.jobs) == 0 {
		m.mode = ModeLO
		m.stats.Restores++
	}

	// Step 3: release jobs due at t.
	for _, tk := range m.taskList {
		spec := tk.spec
		if t < spec.Phi || (t-spec.Phi)%spec.T != 0 {
			continue
		}
		k := (t - spec.Phi) / spec.T
		if m.mode == ModeHI && spec.Crit == LO {
			m.stats.Skipped++
			continue
		}
		c := spec.CL
		if d, ok := tk.demands[k]; ok {
			c = d
		}
		m.jobs = append(m.jobs, &job{tk: tk, k: k, rem: c, d: t + spec.T})
	}

	// Run the unfinished job with the smallest prio for one tick.
	var best *job
	bestIdx := -1
	for i, j := range m.jobs {
		if best == nil || j.tk.spec.Prio < best.tk.spec.Prio {
			best = j
			bestIdx = i
		}
	}
	if best == nil {
		m.runLog = append(m.runLog, "")
		m.now++
		return
	}
	best.exec++
	best.rem--
	m.runLog = append(m.runLog, best.tk.spec.ID)
	if best.rem == 0 {
		m.jobs = append(m.jobs[:bestIdx], m.jobs[bestIdx+1:]...)
		m.stats.Completed++
	} else if m.mode == ModeLO && best.tk.spec.Crit == HI && best.exec == best.tk.spec.CL {
		// Switch to HI at the end of this tick and discard all
		// unfinished LO jobs.
		m.mode = ModeHI
		m.stats.Switches++
		kept := m.jobs[:0]
		for _, j := range m.jobs {
			if j.tk.spec.Crit == LO {
				m.stats.Discarded++
				continue
			}
			kept = append(kept, j)
		}
		m.jobs = kept
	}
	m.now++
}

// Mode returns the current system mode.
func (m *Monitor) Mode() Mode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode
}

// Stats returns a copy of the current counters.
func (m *Monitor) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}

// RunAt returns the id of the task that ran at tick t, or "" if the
// system was idle at t or t is outside [0, now).
func (m *Monitor) RunAt(t int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t < 0 || t >= len(m.runLog) {
		return ""
	}
	return m.runLog[t]
}
