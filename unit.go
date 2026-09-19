package rollback

import "sync"

// Action is a forward unit step.
type Action func() error

// Compensation undoes one successful Action.
type Compensation func() error

// Unit state.
const (
	stateOpen       = 0 // still executing forward steps
	stateCommitted  = 1 // all steps succeeded and committed
	stateRolling    = 2 // a rollback is running
	stateRolledBack = 3 // rollback finished (success or aggregate failure)
)

type registered struct {
	action       Action
	compensation Compensation
}

// StepDef pairs a forward action with its compensation for Unit.Run.
type StepDef struct {
	Action       Action
	Compensation Compensation
}

// Unit is an ordered, sequential unit of work. Each executed step has a paired
// compensation registered before its action runs, so a successful step can
// always be undone exactly once.
type Unit struct {
	store *Store

	mu sync.Mutex

	state      int
	steps      []registered
	traced     []int // compensation trace, in execution order
	rollDone   sync.Cond
	rollResult error
}

// NewUnit returns an open unit bound to store.
func NewUnit(store *Store) *Unit {
	u := &Unit{store: store}
	u.rollDone.L = &u.mu
	return u
}

// Store returns the underlying store.
func (u *Unit) Store() *Store { return u.store }

// Step runs one forward step. The compensation is registered first; only a
// successful action leaves the step eligible for rollback. A failed action
// triggers rollback immediately and returns the wrapped action error.
func (u *Unit) Step(action Action, compensation Compensation) error {
	u.mu.Lock()
	if u.state != stateOpen {
		err := u.stateErrorLocked()
		u.mu.Unlock()
		return err
	}
	index := len(u.steps)
	u.steps = append(u.steps, registered{action: action, compensation: compensation})
	u.mu.Unlock()

	var actionErr error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				actionErr = &ActionPanic{Step: index + 1, Value: recovered}
			}
		}()
		actionErr = action()
	}()

	if actionErr != nil {
		// The action did not take effect, so its own compensation must never
		// run; drop the just-registered entry before rolling back.
		u.mu.Lock()
		u.steps = u.steps[:index]
		u.mu.Unlock()

		rollbackErr := u.Rollback()
		return &StepFailure{Step: index + 1, Err: actionErr, RollbackErr: rollbackErr}
	}
	return nil
}

// Commit finalizes a fully successful unit. Afterwards Rollback is rejected
// with ErrCommitted and no compensation ever runs.
func (u *Unit) Commit() error {
	u.mu.Lock()
	defer u.mu.Unlock()

	switch u.state {
	case stateOpen:
		u.state = stateCommitted
		return nil
	case stateCommitted:
		return ErrCommitted
	default:
		return u.stateErrorLocked()
	}
}

// Run executes steps in order, stopping at the first action failure (which
// triggers rollback), and commits when every step succeeds.
func (u *Unit) Run(defs ...StepDef) error {
	for _, def := range defs {
		if err := u.Step(def.Action, def.Compensation); err != nil {
			return err
		}
	}
	return u.Commit()
}

// Trace returns a copy of the compensation execution trace: step numbers in
// the order their compensations actually ran.
func (u *Unit) Trace() []int {
	u.mu.Lock()
	defer u.mu.Unlock()

	out := make([]int, len(u.traced))
	copy(out, u.traced)
	return out
}

func (u *Unit) stateErrorLocked() error {
	switch u.state {
	case stateCommitted:
		return ErrCommitted
	case stateRolling:
		return ErrInProgress
	case stateRolledBack:
		return ErrRolledBack
	default:
		return nil
	}
}

// StepFailure wraps the failed forward action together with whatever the
// rollback returned.
type StepFailure struct {
	Step        int
	Err         error
	RollbackErr error
}

func (e *StepFailure) Error() string {
	if e.RollbackErr == nil {
		return e.Err.Error()
	}
	return e.Err.Error() + "; rollback reported: " + e.RollbackErr.Error()
}

func (e *StepFailure) Unwrap() error { return e.Err }
