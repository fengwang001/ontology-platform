// Package canfsm implements the fault confinement state machine of a CAN
// controller: it tracks the transmit error counter (TEC) and receive error
// counter (REC), derives the node state (error active, error passive,
// bus-off) from them, and models bus-off recovery.
package canfsm

import (
	"errors"
	"sync"
)

// State is the fault confinement state of the node, derived from TEC/REC.
type State int

const (
	// StateErrorActive: TEC <= 127 and REC <= 127.
	StateErrorActive State = iota
	// StateErrorPassive: TEC > 127 or REC > 127, and TEC <= 255.
	StateErrorPassive
	// StateBusOff: TEC > 255.
	StateBusOff
)

func (s State) String() string {
	switch s {
	case StateErrorActive:
		return "ErrorActive"
	case StateErrorPassive:
		return "ErrorPassive"
	case StateBusOff:
		return "BusOff"
	default:
		return "Unknown"
	}
}

// Event is a transmit/receive event applied to the counters.
type Event int

const (
	// TxOK: successful transmission; TEC -= 1 if TEC > 0.
	TxOK Event = iota
	// TxErr: transmit error; TEC += 8.
	TxErr
	// TxAckErr: acknowledge error during transmission; TEC += 8 if the
	// state before the event is error active, no change if error passive.
	TxAckErr
	// RxOK: successful reception; REC = 127 if REC > 127, else REC -= 1
	// if REC > 0.
	RxOK
	// RxErr: receive error; REC += 1.
	RxErr
	// RxErrDominant: receive error with dominant bit detected; REC += 8.
	RxErrDominant
)

func (e Event) String() string {
	switch e {
	case TxOK:
		return "TxOK"
	case TxErr:
		return "TxErr"
	case TxAckErr:
		return "TxAckErr"
	case RxOK:
		return "RxOK"
	case RxErr:
		return "RxErr"
	case RxErrDominant:
		return "RxErrDominant"
	default:
		return "Invalid"
	}
}

func validEvent(e Event) bool {
	return e >= TxOK && e <= RxErrDominant
}

// Rejection reasons. Each rejected operation leaves counters, recovery
// progress, transition log and the success sequence untouched.
var (
	// ErrInvalidEvent: Apply called with an unknown event.
	ErrInvalidEvent = errors.New("canfsm: invalid event")
	// ErrBusOff: Apply called while the node is bus-off.
	ErrBusOff = errors.New("canfsm: node is bus-off")
	// ErrNotBusOff: Restart called while the node is not bus-off.
	ErrNotBusOff = errors.New("canfsm: node is not bus-off")
	// ErrAlreadyRecovering: Restart called while recovery is in progress.
	ErrAlreadyRecovering = errors.New("canfsm: recovery already in progress")
	// ErrNotRecovering: Idle11 called while no recovery is in progress.
	ErrNotRecovering = errors.New("canfsm: recovery not in progress")
)

// Transition records one state change. Op is the 1-based index of the
// successful operation (Apply, Restart or Idle11) that caused it.
type Transition struct {
	Op   int
	From State
	To   State
}

// Controller is a CAN fault confinement state machine. It is safe for
// concurrent use; every operation and query is linearizable.
type Controller struct {
	mu            sync.Mutex
	tec           int
	rec           int
	recovering    bool
	recoveryCount int
	opSeq         int
	transitions   []Transition
}

// New returns a Controller with TEC = REC = 0 in error active state.
func New() *Controller {
	return &Controller{}
}

// stateLocked derives the state from the counters. Caller must hold mu.
func (c *Controller) stateLocked() State {
	switch {
	case c.tec > 255:
		return StateBusOff
	case c.tec > 127 || c.rec > 127:
		return StateErrorPassive
	default:
		return StateErrorActive
	}
}

// recordTransitionLocked appends a transition if the state changed.
// Caller must hold mu and must have already incremented opSeq.
func (c *Controller) recordTransitionLocked(from State) {
	to := c.stateLocked()
	if to != from {
		c.transitions = append(c.transitions, Transition{Op: c.opSeq, From: from, To: to})
	}
}

// Apply processes one transmit/receive event according to the state
// before the event. It fails with ErrInvalidEvent if the event is
// unknown (checked first), or with ErrBusOff if the node is bus-off.
func (c *Controller) Apply(ev Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !validEvent(ev) {
		return ErrInvalidEvent
	}
	before := c.stateLocked()
	if before == StateBusOff {
		return ErrBusOff
	}

	switch ev {
	case TxOK:
		if c.tec > 0 {
			c.tec--
		}
	case TxErr:
		c.tec += 8
	case TxAckErr:
		if before == StateErrorActive {
			c.tec += 8
		}
	case RxOK:
		if c.rec > 127 {
			c.rec = 127
		} else if c.rec > 0 {
			c.rec--
		}
	case RxErr:
		c.rec++
	case RxErrDominant:
		c.rec += 8
	}

	c.opSeq++
	c.recordTransitionLocked(before)
	return nil
}

// Restart begins bus-off recovery. It fails with ErrNotBusOff if the
// node is not bus-off (checked first), or with ErrAlreadyRecovering if
// recovery has already started.
func (c *Controller) Restart() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stateLocked() != StateBusOff {
		return ErrNotBusOff
	}
	if c.recovering {
		return ErrAlreadyRecovering
	}

	c.recovering = true
	c.recoveryCount = 0
	c.opSeq++
	return nil
}

// Idle11 observes 11 recessive (idle) bits on the bus during recovery.
// It fails with ErrNotRecovering if no recovery is in progress. On the
// 128th call TEC and REC are reset to 0, the node returns to error
// active and recovery ends.
func (c *Controller) Idle11() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.recovering {
		return ErrNotRecovering
	}

	c.recoveryCount++
	c.opSeq++
	if c.recoveryCount == 128 {
		before := c.stateLocked()
		c.tec = 0
		c.rec = 0
		c.recovering = false
		c.recordTransitionLocked(before)
	}
	return nil
}

// Snapshot is a consistent view of the controller.
type Snapshot struct {
	TEC           int
	REC           int
	State         State
	Recovering    bool
	RecoveryCount int
	OpSeq         int
}

// Snapshot returns a consistent snapshot of counters, state and
// recovery progress.
func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Snapshot{
		TEC:           c.tec,
		REC:           c.rec,
		State:         c.stateLocked(),
		Recovering:    c.recovering,
		RecoveryCount: c.recoveryCount,
		OpSeq:         c.opSeq,
	}
}

// TEC returns the transmit error counter.
func (c *Controller) TEC() int {
	return c.Snapshot().TEC
}

// REC returns the receive error counter.
func (c *Controller) REC() int {
	return c.Snapshot().REC
}

// State returns the state derived from the counters.
func (c *Controller) State() State {
	return c.Snapshot().State
}

// Recovering reports whether bus-off recovery is in progress.
func (c *Controller) Recovering() bool {
	return c.Snapshot().Recovering
}

// RecoveryCount returns the number of Idle11 observations so far.
func (c *Controller) RecoveryCount() int {
	return c.Snapshot().RecoveryCount
}

// Transitions returns a copy of the state transition log.
func (c *Controller) Transitions() []Transition {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Transition, len(c.transitions))
	copy(out, c.transitions)
	return out
}
