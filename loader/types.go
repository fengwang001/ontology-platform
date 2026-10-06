// Package loader implements a browser-style resource loading scheduler.
//
// The scheduler coordinates five concerns: request registration, per-origin
// connection quotas, priority preemption, a preload cache, and completion
// notification. All mutating operations take a logical timestamp and are
// serialized by a single mutex, so concurrent callers observe a result
// equivalent to some serial order.
package loader

import "fmt"

// ResourceType classifies a resource request.
type ResourceType int

const (
	TypeDocument ResourceType = iota
	TypeStyle
	TypeScript
	TypeFont
	TypeImage
	TypePreload
	typeCount
)

func (t ResourceType) valid() bool { return t >= 0 && t < typeCount }

func (t ResourceType) String() string {
	switch t {
	case TypeDocument:
		return "document"
	case TypeStyle:
		return "style"
	case TypeScript:
		return "script"
	case TypeFont:
		return "font"
	case TypeImage:
		return "image"
	case TypePreload:
		return "preload"
	}
	return fmt.Sprintf("ResourceType(%d)", int(t))
}

// Priority of a request; PriorityHighest is the only level that preempts.
type Priority int

const (
	PriorityLowest Priority = iota
	PriorityLow
	PriorityMedium
	PriorityHigh
	PriorityHighest
)

func (p Priority) valid() bool { return p >= PriorityLowest && p <= PriorityHighest }

func (p Priority) String() string {
	switch p {
	case PriorityLowest:
		return "lowest"
	case PriorityLow:
		return "low"
	case PriorityMedium:
		return "medium"
	case PriorityHigh:
		return "high"
	case PriorityHighest:
		return "highest"
	}
	return fmt.Sprintf("Priority(%d)", int(p))
}

// CredentialsMode mirrors fetch credentials modes.
type CredentialsMode string

const (
	CredentialsOmit       CredentialsMode = "omit"
	CredentialsSameOrigin CredentialsMode = "same-origin"
	CredentialsInclude    CredentialsMode = "include"
)

// Origin identifies a source by scheme, host and port.
type Origin struct {
	Scheme string
	Host   string
	Port   uint16
}

func (o Origin) String() string { return fmt.Sprintf("%s://%s:%d", o.Scheme, o.Host, o.Port) }

// State is the lifecycle state of a request. Terminal states are
// StateDone, StateFailed and StateAborted; they are mutually exclusive
// and irreversible.
type State int

const (
	StatePending State = iota
	StateInFlight
	StateAttached
	StateDone
	StateFailed
	StateAborted
)

// Terminal reports whether s is a terminal state.
func (s State) Terminal() bool { return s >= StateDone }

func (s State) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateInFlight:
		return "in-flight"
	case StateAttached:
		return "attached"
	case StateDone:
		return "done"
	case StateFailed:
		return "failed"
	case StateAborted:
		return "aborted"
	}
	return fmt.Sprintf("State(%d)", int(s))
}

// ErrorKind classifies rejected operations and abort propagation.
type ErrorKind int

const (
	ErrInvalidArgument ErrorKind = iota
	ErrClockRewind
	ErrNotFound
	ErrIllegalState
	ErrAborted
)

func (k ErrorKind) String() string {
	switch k {
	case ErrInvalidArgument:
		return "invalid-argument"
	case ErrClockRewind:
		return "clock-rewind"
	case ErrNotFound:
		return "not-found"
	case ErrIllegalState:
		return "illegal-state"
	case ErrAborted:
		return "aborted"
	}
	return fmt.Sprintf("ErrorKind(%d)", int(k))
}

// Error is a rejected operation or a failure cause.
type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Kind, e.Msg) }

func errInvalid(msg string) *Error  { return &Error{Kind: ErrInvalidArgument, Msg: msg} }
func errRewind(msg string) *Error   { return &Error{Kind: ErrClockRewind, Msg: msg} }
func errNotFound(msg string) *Error { return &Error{Kind: ErrNotFound, Msg: msg} }
func errState(msg string) *Error    { return &Error{Kind: ErrIllegalState, Msg: msg} }

// Config holds the tunable limits of a Scheduler.
type Config struct {
	// PerOriginLimit caps simultaneously in-flight requests per origin. Must be > 0.
	PerOriginLimit int
	// GlobalLimit caps simultaneously in-flight requests overall. Must be > 0.
	GlobalLimit int
	// MaxPauses caps how many times a single request may be preempted.
	MaxPauses int
	// PreloadTTL is the number of clock ticks a completed, unused preload
	// cache entry may live; an entry is wasted once its age exceeds it.
	PreloadTTL int64
}

// EventKind classifies scheduler notifications.
type EventKind int

const (
	EventRegistered EventKind = iota
	EventStarted
	EventResumed
	EventPaused
	EventAttached
	EventCompleted
	EventFailed
	EventAborted
	EventCacheHit
	EventWasted
)

func (k EventKind) String() string {
	switch k {
	case EventRegistered:
		return "registered"
	case EventStarted:
		return "started"
	case EventResumed:
		return "resumed"
	case EventPaused:
		return "paused"
	case EventAttached:
		return "attached"
	case EventCompleted:
		return "completed"
	case EventFailed:
		return "failed"
	case EventAborted:
		return "aborted"
	case EventCacheHit:
		return "cache-hit"
	case EventWasted:
		return "wasted"
	}
	return fmt.Sprintf("EventKind(%d)", int(k))
}

// Event is a single scheduler notification delivered to subscribers.
type Event struct {
	Time     int64
	Kind     EventKind
	ID       uint64
	Origin   Origin
	URL      string
	Priority Priority
	Progress int64
	Target   uint64
}

// Snapshot is an immutable view of a request at query time. Once a request
// reaches a terminal state, every Query returns the same Snapshot.
type Snapshot struct {
	ID        uint64
	State     State
	Priority  Priority
	Progress  int64
	Size      int64
	Pauses    int
	FromCache bool
	HasErr    bool
	ErrKind   ErrorKind
	AttachTo  uint64
}

// Stats reports scheduler counters at a point in time.
type Stats struct {
	Now               int64
	InFlightTotal     int
	InFlightPerOrigin map[Origin]int
	Pending           int
	CacheSize         int
	Wasted            int
}
