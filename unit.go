package ontology

import (
	"errors"
	"sync"
)

// Action is a forward step. Compensation is its paired undo.
type Action func() error

// Compensation undoes one successful step. It may fail or panic.
type Compensation func() error

type step struct {
	n      int
	action Action
	comp   Compensation
}

const (
	phasePending   = iota // no outcome yet
	phaseRunning          // rollback is executing
	phaseDone             // rollback finished
	phaseCommitted        // unit committed; rollback forbidden
)

// Unit executes steps in order and compensates them in reverse on failure.
type Unit struct {
	store *Store

	mu       sync.Mutex
	steps    []step
	trace    []int
	phase    int
	done     chan struct{}
	result   error
	ran      bool
	successN int // number of steps whose action succeeded
}

// NewUnit creates a work unit backed by store.
func NewUnit(store *Store) *Unit {
	return &Unit{store: store, done: make(chan struct{})}
}

// Add registers one step paired with its compensation.
// Step numbers start at 1, in registration order.
func (u *Unit) Add(action Action, comp Compensation) *Unit {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.steps = append(u.steps, step{
		n:      len(u.steps) + 1,
		action: action,
		comp:   comp,
	})
	return u
}

// Run executes every step in order. On the first failure it rolls back all
// successful steps in reverse. It returns nil only when all steps committed.
func (u *Unit) Run() error {
	u.mu.Lock()
	steps := make([]step, len(u.steps))
	copy(steps, u.steps)
	u.ran = true
	u.mu.Unlock()

	for _, st := range steps {
		if err := st.action(); err != nil {
			rbErr := u.Rollback()
			return errors.Join(err, rbErr)
		}
		u.mu.Lock()
		u.successN++
		u.mu.Unlock()
	}
	return u.Commit()
}

// Commit marks the unit successfully committed.
func (u *Unit) Commit() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.phase == phaseCommitted {
		return nil
	}
	if u.phase == phaseDone || u.phase == phaseRunning {
		return &RollbackRejectedError{Reason: "unit already rolled back"}
	}
	u.phase = phaseCommitted
	return nil
}

// Rollback executes every applicable compensation exactly once, in strict
// reverse step order. Concurrent calls share one single execution.
func (u *Unit) Rollback() error {
	u.mu.Lock()
	switch u.phase {
	case phaseCommitted:
		u.mu.Unlock()
		return &RollbackRejectedError{Reason: "unit already committed"}
	case phaseDone:
		ch := u.done
		u.mu.Unlock()
		<-ch
		return u.result
	case phaseRunning:
		ch := u.done
		u.mu.Unlock()
		<-ch
		return u.result
	}
	u.phase = phaseRunning
	u.mu.Unlock()

	u.executeRollback()

	u.mu.Lock()
	u.phase = phaseDone
	res := u.result
	u.mu.Unlock()
	close(u.done)
	return res
}

// Trace returns compensation step numbers in execution order.
func (u *Unit) Trace() []int {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]int, len(u.trace))
	copy(out, u.trace)
	return out
}

func (u *Unit) executeRollback() {
	u.mu.Lock()
	limit := len(u.steps)
	if u.ran {
		limit = u.successN
	}
	steps := make([]step, limit)
	copy(steps, u.steps[:limit])
	u.mu.Unlock()

	var failures []StepError
	for i := len(steps) - 1; i >= 0; i-- {
		st := steps[i]
		err := u.runCompensation(st.comp)
		u.mu.Lock()
		u.trace = append(u.trace, st.n)
		u.mu.Unlock()
		if err != nil {
			failures = append(failures, StepError{Step: st.n, Err: err})
			u.store.Taint(st.n)
		}
	}
	if len(failures) > 0 {
		u.result = &AggregateError{failures: failures}
	}
}

// runCompensation converts a panic into a *PanicError so later compensations
// keep running.
func (u *Unit) runCompensation(comp Compensation) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Value: r}
		}
	}()
	return comp()
}
