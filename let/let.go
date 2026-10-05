// Package let implements Logical Execution Time (LET) end-to-end latency
// analysis for periodic task chains with release-read / delayed-write
// semantics.
//
// A task's j-th job (j = 0, 1, ...) is released and reads its inputs at
// r = phi + j*T, and writes its output at r + w. A value written at time u
// is visible to any read released at r >= u (same-instant visibility).
package let

import (
	"errors"
	"sync"
)

// Limits enforced by the analyzer.
const (
	MaxTasks       = 16   // maximum number of tasks
	MaxTaskIDLen   = 32   // maximum task ID length in bytes
	MinPeriod      = 1    // minimum period
	MaxPeriod      = 1000 // maximum period
	MinChainLen    = 2    // minimum tasks per chain
	MaxChainLen    = 6    // maximum tasks per chain
	MaxHyperperiod = 5000 // maximum lcm of chain periods
	MaxTuneProduct = 1000 // maximum product of T2..Tn for Tune
)

// Rejection reasons, checked in the order listed here; only the first
// applicable reason is reported.
var (
	ErrInvalidArgument = errors.New("let: invalid argument")
	ErrNotFound        = errors.New("let: task or chain does not exist")
	ErrDuplicate       = errors.New("let: duplicate identifier")
	ErrCapacity        = errors.New("let: capacity full")
	ErrTooLarge        = errors.New("let: size limit exceeded")
	ErrInUse           = errors.New("let: task is referenced by a chain")
	ErrShared          = errors.New("let: chain shares a task with another chain")
)

// Task is a periodic LET task. The j-th job is released at Phi+j*T and
// writes its output at Phi+j*T+W. W == T is pure LET semantics; a smaller
// W models implicit communication with a fixed response time.
type Task struct {
	ID  string // non-empty, at most 32 bytes
	T   int    // period, 1..1000
	Phi int    // phase, 0 <= Phi < T
	W   int    // write delay, 1 <= W <= T
}

// Result holds the end-to-end latency metrics of a chain.
type Result struct {
	MaxReaction int
	MinReaction int
	MaxAge      int
}

// TuneResult reports the outcome of a Tune call. When Changed is false the
// "after" values equal the "before" values and no phase was modified.
type TuneResult struct {
	Changed           bool
	MaxReactionBefore int
	MaxReactionAfter  int
	PhasesBefore      []int // phases of tau1..taun before tuning
	PhasesAfter       []int // phases of tau1..taun after tuning
}

// Analyzer stores tasks and chains. All methods are safe for concurrent
// use; the result of concurrent calls is equivalent to some serial order.
type Analyzer struct {
	mu     sync.RWMutex
	tasks  map[string]*Task
	chains map[string][]string // chain name -> ordered task IDs
}

// NewAnalyzer returns an empty Analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{
		tasks:  make(map[string]*Task),
		chains: make(map[string][]string),
	}
}

// validateTask checks the parameter rules for a task definition.
func validateTask(t Task) error {
	if t.ID == "" || len(t.ID) > MaxTaskIDLen {
		return ErrInvalidArgument
	}
	if t.T < MinPeriod || t.T > MaxPeriod {
		return ErrInvalidArgument
	}
	if t.Phi < 0 || t.Phi >= t.T {
		return ErrInvalidArgument
	}
	if t.W < 1 || t.W > t.T {
		return ErrInvalidArgument
	}
	return nil
}

// AddTask registers a new task.
func (a *Analyzer) AddTask(t Task) error {
	if err := validateTask(t); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.tasks[t.ID]; ok {
		return ErrDuplicate
	}
	if len(a.tasks) >= MaxTasks {
		return ErrCapacity
	}
	task := t
	a.tasks[t.ID] = &task
	return nil
}

// RemoveTask deletes a task that is not referenced by any chain.
func (a *Analyzer) RemoveTask(id string) error {
	if id == "" {
		return ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.tasks[id]; !ok {
		return ErrNotFound
	}
	for _, ids := range a.chains {
		for _, tid := range ids {
			if tid == id {
				return ErrInUse
			}
		}
	}
	delete(a.tasks, id)
	return nil
}

// SetPhase changes a task's phase to phi (0 <= phi < T).
func (a *Analyzer) SetPhase(id string, phi int) error {
	if id == "" || phi < 0 || phi >= MaxPeriod {
		return ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.tasks[id]
	if !ok {
		return ErrNotFound
	}
	if phi >= t.T {
		return ErrInvalidArgument
	}
	t.Phi = phi
	return nil
}

// SetDelay changes a task's write delay to w (1 <= w <= T).
func (a *Analyzer) SetDelay(id string, w int) error {
	if id == "" || w < 1 || w > MaxPeriod {
		return ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.tasks[id]
	if !ok {
		return ErrNotFound
	}
	if w > t.T {
		return ErrInvalidArgument
	}
	t.W = w
	return nil
}

// AddChain defines a chain tau1 -> ... -> taun over existing, pairwise
// distinct tasks.
func (a *Analyzer) AddChain(name string, ids []string) error {
	if name == "" || len(ids) < MinChainLen || len(ids) > MaxChainLen {
		return ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range ids {
		if _, ok := a.tasks[id]; !ok {
			return ErrNotFound
		}
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return ErrDuplicate
		}
		seen[id] = true
	}
	if _, ok := a.chains[name]; ok {
		return ErrDuplicate
	}
	if h := hyperperiod(a.chainTasksLocked(ids)); h > MaxHyperperiod {
		return ErrTooLarge
	}
	a.chains[name] = append([]string(nil), ids...)
	return nil
}

// RemoveChain deletes a chain definition (the tasks remain).
func (a *Analyzer) RemoveChain(name string) error {
	if name == "" {
		return ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.chains[name]; !ok {
		return ErrNotFound
	}
	delete(a.chains, name)
	return nil
}

// chainTasksLocked resolves a chain's task IDs to task snapshots.
// Callers must hold the lock.
func (a *Analyzer) chainTasksLocked(ids []string) []Task {
	ts := make([]Task, len(ids))
	for i, id := range ids {
		ts[i] = *a.tasks[id]
	}
	return ts
}
