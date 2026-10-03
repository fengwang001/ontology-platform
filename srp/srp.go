// Package srp implements a Stack Resource Policy (SRP) start gate for
// multi-unit resources.
//
// Preemption levels (pi) are derived from the relative deadlines of all
// currently declared tasks: distinct deadlines are sorted descending, the
// largest deadline gets pi=1, the next pi=2, and so on; equal deadlines
// share the same level. Levels are recomputed whenever tasks are added or
// removed.
//
// Resource ceilings are dynamic: Ceil(r) = max{ pi_k : mu_{k,r} > avail_r }
// (strictly greater), or 0 when no task's declared demand exceeds the
// current availability. SysCeil is the maximum resource ceiling (0 when no
// resources exist).
//
// Running jobs form a stack. Start is the only gate: a job may start only
// when its task's level is strictly greater than the level of the stack
// top's task and strictly greater than the current SysCeil. Once started,
// a job's Acquire/Release/Finish are never blocked by other jobs, only by
// stack-top order and its own declared demands.
package srp

import (
	"errors"
	"fmt"
	"sync"
)

// Limits of the manager.
const (
	MaxResources = 8
	MaxTasks     = 16
	MaxJobs      = 32
	MaxUnits     = 1000
	MaxDeadline  = 1_000_000
	MaxMuEntries = 4
)

// Rejection reasons, listed in the exact precedence order in which they are
// reported: for every operation the applicable checks are evaluated in this
// order and only the first matching reason is returned.
var (
	ErrInvalidParam      = errors.New("srp: invalid parameter")
	ErrNotFound          = errors.New("srp: task, job or resource does not exist")
	ErrDuplicate         = errors.New("srp: duplicate identifier")
	ErrCapacity          = errors.New("srp: capacity reached")
	ErrNotTop            = errors.New("srp: job is not the stack top")
	ErrLevelInsufficient = errors.New("srp: preemption level not greater than stack top")
	ErrCeilingBlocked    = errors.New("srp: preemption level not greater than system ceiling")
	ErrExceedsDeclared   = errors.New("srp: acquire exceeds declared maximum")
	ErrInsufficientUnits = errors.New("srp: not enough available units")
	ErrNotHeld           = errors.New("srp: release exceeds held units")
	ErrStillHolding      = errors.New("srp: job still holds resources")
	ErrTaskInUse         = errors.New("srp: task has unfinished jobs")
)

// MuEntry declares the maximum number of units of one resource a task may
// hold simultaneously. Resources not listed for a task have mu = 0.
type MuEntry struct {
	Resource string
	Units    int
}

type resource struct {
	id    string
	n     int
	avail int
}

type task struct {
	id string
	d  int
	mu map[string]int
}

type job struct {
	id     string
	taskID string
	held   map[string]int
}

// Manager is a concurrency-safe SRP start gate. All operations and queries
// are linearizable: concurrent calls behave as if executed in some serial
// order. The zero value is not usable; use NewManager.
type Manager struct {
	mu sync.Mutex

	resources map[string]*resource
	resOrder  []string
	tasks     map[string]*task
	jobs      map[string]*job
	stack     []*job

	// insufficientUnitsCount counts how often Acquire failed with
	// ErrInsufficientUnits. Under legal operation sequences where every job
	// respects its declared mu, the SRP start gate guarantees this stays 0.
	insufficientUnitsCount int
}

// NewManager returns an empty Manager.
func NewManager() *Manager {
	return &Manager{
		resources: make(map[string]*resource),
		tasks:     make(map[string]*task),
		jobs:      make(map[string]*job),
	}
}

// DeclareResource registers a resource with n total units (1..MaxUnits).
func (m *Manager) DeclareResource(id string, n int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.declareResource(id, n)
}

// AddTask registers a task with relative deadline d (1..MaxDeadline) and a
// demand table mu of at most MaxMuEntries entries. Adding or removing tasks
// re-derives all preemption levels.
func (m *Manager) AddTask(id string, d int, mu []MuEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addTask(id, d, mu)
}

// RemoveTask deletes a task. It fails with ErrTaskInUse while any running
// job belongs to the task.
func (m *Manager) RemoveTask(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.removeTask(id)
}

// Start pushes a new job of the given task onto the stack. The task's level
// must be strictly greater than the stack top's level (no requirement when
// the stack is empty) and strictly greater than the current SysCeil.
func (m *Manager) Start(jobID, taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.start(jobID, taskID)
}

// Acquire takes u units (1..MaxUnits) of a resource for the stack-top job.
func (m *Manager) Acquire(jobID, resourceID string, u int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.acquire(jobID, resourceID, u)
}

// Release returns u units of a resource from the stack-top job.
func (m *Manager) Release(jobID, resourceID string, u int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.release(jobID, resourceID, u)
}

// Finish pops the stack-top job, which must hold no resources.
func (m *Manager) Finish(jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.finish(jobID)
}

// Level returns the current preemption level of a task (1-based; the
// largest deadline has level 1).
func (m *Manager) Level(taskID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[taskID]
	if !ok {
		return 0, fmt.Errorf("%w: task %q", ErrNotFound, taskID)
	}
	return m.levelOf(t.d), nil
}

// Ceil returns the current ceiling of a resource.
func (m *Manager) Ceil(resourceID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.resources[resourceID]
	if !ok {
		return 0, fmt.Errorf("%w: resource %q", ErrNotFound, resourceID)
	}
	return m.ceilOf(r), nil
}

// SysCeil returns the current system ceiling: the maximum resource
// ceiling, or 0 when no resources exist.
func (m *Manager) SysCeil() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sysCeil()
}

// Avail returns the currently available units of a resource.
func (m *Manager) Avail(resourceID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.resources[resourceID]
	if !ok {
		return 0, fmt.Errorf("%w: resource %q", ErrNotFound, resourceID)
	}
	return r.avail, nil
}

// Stack returns the job ids of the running stack, bottom first.
func (m *Manager) Stack() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.stack))
	for i, j := range m.stack {
		out[i] = j.id
	}
	return out
}

// --- internal skeletons, filled in incrementally ---

func (m *Manager) declareResource(id string, n int) error {
	if id == "" || n < 1 || n > MaxUnits {
		return fmt.Errorf("%w: resource %q n=%d", ErrInvalidParam, id, n)
	}
	if _, ok := m.resources[id]; ok {
		return fmt.Errorf("%w: resource %q", ErrDuplicate, id)
	}
	if len(m.resources) >= MaxResources {
		return fmt.Errorf("%w: resources", ErrCapacity)
	}
	m.resources[id] = &resource{id: id, n: n, avail: n}
	m.resOrder = append(m.resOrder, id)
	return nil
}

func (m *Manager) addTask(id string, d int, mu []MuEntry) error {
	if id == "" || d < 1 || d > MaxDeadline || len(mu) > MaxMuEntries {
		return fmt.Errorf("%w: task %q d=%d mu entries=%d", ErrInvalidParam, id, d, len(mu))
	}
	seen := make(map[string]bool, len(mu))
	for _, e := range mu {
		if e.Resource == "" || e.Units < 1 || e.Units > MaxUnits {
			return fmt.Errorf("%w: task %q mu entry %+v", ErrInvalidParam, id, e)
		}
		if seen[e.Resource] {
			return fmt.Errorf("%w: task %q duplicate mu resource %q", ErrInvalidParam, id, e.Resource)
		}
		seen[e.Resource] = true
	}
	for _, e := range mu {
		r, ok := m.resources[e.Resource]
		if !ok {
			return fmt.Errorf("%w: resource %q", ErrNotFound, e.Resource)
		}
		if e.Units > r.n {
			return fmt.Errorf("%w: task %q mu %d exceeds resource %q total %d",
				ErrInvalidParam, id, e.Units, e.Resource, r.n)
		}
	}
	if _, ok := m.tasks[id]; ok {
		return fmt.Errorf("%w: task %q", ErrDuplicate, id)
	}
	if len(m.tasks) >= MaxTasks {
		return fmt.Errorf("%w: tasks", ErrCapacity)
	}
	table := make(map[string]int, len(mu))
	for _, e := range mu {
		table[e.Resource] = e.Units
	}
	m.tasks[id] = &task{id: id, d: d, mu: table}
	return nil
}

func (m *Manager) removeTask(id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty task id", ErrInvalidParam)
	}
	if _, ok := m.tasks[id]; !ok {
		return fmt.Errorf("%w: task %q", ErrNotFound, id)
	}
	for _, j := range m.stack {
		if j.taskID == id {
			return fmt.Errorf("%w: task %q", ErrTaskInUse, id)
		}
	}
	delete(m.tasks, id)
	return nil
}

func (m *Manager) start(jobID, taskID string) error {
	if jobID == "" || taskID == "" {
		return fmt.Errorf("%w: empty id", ErrInvalidParam)
	}
	t, ok := m.tasks[taskID]
	if !ok {
		return fmt.Errorf("%w: task %q", ErrNotFound, taskID)
	}
	if _, ok := m.jobs[jobID]; ok {
		return fmt.Errorf("%w: job %q", ErrDuplicate, jobID)
	}
	if len(m.stack) >= MaxJobs {
		return fmt.Errorf("%w: jobs", ErrCapacity)
	}
	level := m.levelOf(t.d)
	if n := len(m.stack); n > 0 {
		top := m.tasks[m.stack[n-1].taskID]
		if topLevel := m.levelOf(top.d); level <= topLevel {
			return fmt.Errorf("%w: level %d <= stack top level %d",
				ErrLevelInsufficient, level, topLevel)
		}
	}
	if ceil := m.sysCeil(); level <= ceil {
		return fmt.Errorf("%w: level %d <= system ceiling %d",
			ErrCeilingBlocked, level, ceil)
	}
	j := &job{id: jobID, taskID: taskID, held: make(map[string]int)}
	m.jobs[jobID] = j
	m.stack = append(m.stack, j)
	return nil
}

func (m *Manager) acquire(jobID, resourceID string, u int) error {
	if jobID == "" || resourceID == "" || u < 1 || u > MaxUnits {
		return fmt.Errorf("%w: job %q resource %q u=%d", ErrInvalidParam, jobID, resourceID, u)
	}
	j, ok := m.jobs[jobID]
	if !ok {
		return fmt.Errorf("%w: job %q", ErrNotFound, jobID)
	}
	r, ok := m.resources[resourceID]
	if !ok {
		return fmt.Errorf("%w: resource %q", ErrNotFound, resourceID)
	}
	if m.stack[len(m.stack)-1] != j {
		return fmt.Errorf("%w: job %q", ErrNotTop, jobID)
	}
	declared := m.tasks[j.taskID].mu[resourceID]
	if j.held[resourceID]+u > declared {
		return fmt.Errorf("%w: job %q held %d + %d > declared %d",
			ErrExceedsDeclared, jobID, j.held[resourceID], u, declared)
	}
	if u > r.avail {
		m.insufficientUnitsCount++
		return fmt.Errorf("%w: job %q wants %d of %q, only %d available",
			ErrInsufficientUnits, jobID, u, resourceID, r.avail)
	}
	r.avail -= u
	j.held[resourceID] += u
	return nil
}

func (m *Manager) release(jobID, resourceID string, u int) error {
	if jobID == "" || resourceID == "" || u < 1 || u > MaxUnits {
		return fmt.Errorf("%w: job %q resource %q u=%d", ErrInvalidParam, jobID, resourceID, u)
	}
	j, ok := m.jobs[jobID]
	if !ok {
		return fmt.Errorf("%w: job %q", ErrNotFound, jobID)
	}
	r, ok := m.resources[resourceID]
	if !ok {
		return fmt.Errorf("%w: resource %q", ErrNotFound, resourceID)
	}
	if m.stack[len(m.stack)-1] != j {
		return fmt.Errorf("%w: job %q", ErrNotTop, jobID)
	}
	if u > j.held[resourceID] {
		return fmt.Errorf("%w: job %q holds %d of %q, cannot release %d",
			ErrNotHeld, jobID, j.held[resourceID], resourceID, u)
	}
	j.held[resourceID] -= u
	r.avail += u
	return nil
}

func (m *Manager) finish(jobID string) error {
	if jobID == "" {
		return fmt.Errorf("%w: empty job id", ErrInvalidParam)
	}
	j, ok := m.jobs[jobID]
	if !ok {
		return fmt.Errorf("%w: job %q", ErrNotFound, jobID)
	}
	if m.stack[len(m.stack)-1] != j {
		return fmt.Errorf("%w: job %q", ErrNotTop, jobID)
	}
	for _, u := range j.held {
		if u > 0 {
			return fmt.Errorf("%w: job %q", ErrStillHolding, jobID)
		}
	}
	m.stack = m.stack[:len(m.stack)-1]
	delete(m.jobs, jobID)
	return nil
}

// levelOf derives the preemption level of a deadline from the set of
// distinct deadlines of all declared tasks: the largest deadline is
// level 1, the next distinct one level 2, and so on.
func (m *Manager) levelOf(d int) int {
	level := 1
	seen := make(map[int]bool, len(m.tasks))
	for _, t := range m.tasks {
		if t.d > d && !seen[t.d] {
			seen[t.d] = true
			level++
		}
	}
	return level
}

// ceilOf computes the dynamic ceiling of one resource:
// max{ pi_k : mu_{k,r} > avail_r }, or 0 when the set is empty.
func (m *Manager) ceilOf(r *resource) int {
	ceil := 0
	for _, t := range m.tasks {
		if t.mu[r.id] > r.avail {
			if lv := m.levelOf(t.d); lv > ceil {
				ceil = lv
			}
		}
	}
	return ceil
}

func (m *Manager) sysCeil() int {
	ceil := 0
	for _, id := range m.resOrder {
		if c := m.ceilOf(m.resources[id]); c > ceil {
			ceil = c
		}
	}
	return ceil
}
