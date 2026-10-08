// Package vrrp implements a VRRP-style single-device master/backup state
// machine. The caller injects a clock, received advertisements and
// operational events; the device transitions between the init, backup and
// master roles and produces advertisements that the caller must send out.
// Multi-device behaviour is reproduced by feeding the outputs of each
// device into the others as received advertisements.
package vrrp

import "fmt"

const (
	// MaxAdvertIntervalMs is the upper bound for advertisement intervals.
	MaxAdvertIntervalMs = 1_000_000
	// OwnerPriority marks the address owner. It may only be declared at
	// creation time and can never be set to or changed from at runtime.
	OwnerPriority = 255
	// YieldPriority in a received advertisement means the previous master
	// is voluntarily stepping down.
	YieldPriority = 0
)

// Role is the device role.
type Role int

const (
	RoleInit Role = iota
	RoleBackup
	RoleMaster
)

func (r Role) String() string {
	switch r {
	case RoleInit:
		return "init"
	case RoleBackup:
		return "backup"
	case RoleMaster:
		return "master"
	}
	return "unknown"
}

// Advert is an advertisement message. When produced by a device it must be
// delivered (by the caller) to the peer devices as a received
// advertisement.
type Advert struct {
	SenderID   string
	Priority   int
	IntervalMs int
}

func (a Advert) String() string {
	return fmt.Sprintf("advert{id:%q prio:%d interval:%dms}", a.SenderID, a.Priority, a.IntervalMs)
}

// Config is the immutable-at-creation device configuration.
type Config struct {
	// ID is the non-empty, totally ordered device identifier.
	ID string
	// Priority is in [1, 255]; 255 declares the address owner.
	Priority int
	// Preempt enables preemption of lower-priority masters.
	Preempt bool
	// AdvertIntervalMs is the advertisement interval in [1, MaxAdvertIntervalMs].
	AdvertIntervalMs int
}

func (c Config) validate() error {
	if c.ID == "" {
		return &Error{Kind: ErrInvalidArgument, Detail: "device id must not be empty"}
	}
	if c.Priority < 1 || c.Priority > OwnerPriority {
		return &Error{Kind: ErrInvalidArgument, Detail: fmt.Sprintf("priority %d out of range [1,255]", c.Priority)}
	}
	if c.AdvertIntervalMs < 1 || c.AdvertIntervalMs > MaxAdvertIntervalMs {
		return &Error{Kind: ErrInvalidArgument, Detail: fmt.Sprintf("advert interval %d out of range [1,%d]", c.AdvertIntervalMs, MaxAdvertIntervalMs)}
	}
	return nil
}

// ErrKind classifies rejected events. The declaration order is the fixed
// precedence order: invalid argument, clock rollback, not started,
// identifier conflict, address owner conflict.
type ErrKind int

const (
	ErrInvalidArgument ErrKind = iota + 1
	ErrClockRollback
	ErrNotStarted
	ErrIdentifierConflict
	ErrAddressOwnerConflict
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidArgument:
		return "invalid argument"
	case ErrClockRollback:
		return "clock rollback"
	case ErrNotStarted:
		return "not started"
	case ErrIdentifierConflict:
		return "identifier conflict"
	case ErrAddressOwnerConflict:
		return "address owner conflict"
	}
	return "unknown error"
}

// Error is the only error type returned by this package.
type Error struct {
	Kind   ErrKind
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return "vrrp: " + e.Kind.String()
	}
	return "vrrp: " + e.Kind.String() + ": " + e.Detail
}

// EventKind enumerates the injectable events.
type EventKind int

const (
	EvStart EventKind = iota
	EvStop
	EvReceiveAdvert
	EvAdvanceTime
	EvTakeAdvert
	EvSetPriority
	EvSetPreempt
)

func (k EventKind) String() string {
	switch k {
	case EvStart:
		return "start"
	case EvStop:
		return "stop"
	case EvReceiveAdvert:
		return "receive-advert"
	case EvAdvanceTime:
		return "advance-time"
	case EvTakeAdvert:
		return "take-advert"
	case EvSetPriority:
		return "set-priority"
	case EvSetPreempt:
		return "set-preempt"
	}
	return "unknown"
}

// Event is a single input to the state machine. Now is the caller
// supplied clock in non-negative milliseconds; it must not be smaller
// than the time of the previously accepted event. Advert is used by
// EvReceiveAdvert, Priority by EvSetPriority and Preempt by EvSetPreempt.
type Event struct {
	Kind     EventKind
	Now      uint64
	Advert   Advert
	Priority int
	Preempt  bool
}

func (e Event) String() string {
	switch e.Kind {
	case EvReceiveAdvert:
		return fmt.Sprintf("%s@%d %s", e.Kind, e.Now, e.Advert)
	case EvSetPriority:
		return fmt.Sprintf("%s@%d prio:%d", e.Kind, e.Now, e.Priority)
	case EvSetPreempt:
		return fmt.Sprintf("%s@%d preempt:%t", e.Kind, e.Now, e.Preempt)
	}
	return fmt.Sprintf("%s@%d", e.Kind, e.Now)
}

// Start creates a start event.
func Start(now uint64) Event { return Event{Kind: EvStart, Now: now} }

// Stop creates a stop event.
func Stop(now uint64) Event { return Event{Kind: EvStop, Now: now} }

// ReceiveAdvert creates an inbound advertisement event.
func ReceiveAdvert(adv Advert, now uint64) Event {
	return Event{Kind: EvReceiveAdvert, Now: now, Advert: adv}
}

// AdvanceTime creates a time advance event.
func AdvanceTime(now uint64) Event { return Event{Kind: EvAdvanceTime, Now: now} }

// TakeAdvert asks the device for a due periodic advertisement.
func TakeAdvert(now uint64) Event { return Event{Kind: EvTakeAdvert, Now: now} }

// SetPriority creates a priority change event.
func SetPriority(p int, now uint64) Event {
	return Event{Kind: EvSetPriority, Now: now, Priority: p}
}

// SetPreempt creates a preempt switch event.
func SetPreempt(on bool, now uint64) Event {
	return Event{Kind: EvSetPreempt, Now: now, Preempt: on}
}

// Result is the outcome of an accepted event. Adverts holds the
// advertisements the caller must send out (at most one per event).
// Reason is a short human readable justification of the decision, useful
// for logging and auditing.
type Result struct {
	Adverts []Advert
	Reason  string
}

// Snapshot exposes the full observable state of a Device, primarily for
// testing and auditing.
type Snapshot struct {
	Role          Role
	Clock         uint64
	Priority      int
	Preempt       bool
	WatchBase     uint64
	WatchInterval int
	SkewSet       bool
	SkewDeadline  uint64
	NextAdvert    uint64
}
