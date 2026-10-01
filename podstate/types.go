// Package podstate implements a deterministic Pod phase and restart-backoff
// state machine with ordered init containers and parallel app containers.
package podstate

import "sync"

// RestartPolicy decides where a container goes after it exits.
type RestartPolicy int

const (
	Always RestartPolicy = iota
	OnFailure
	Never
)

// State is the lifecycle state of a single container.
type State int

const (
	Waiting State = iota
	Running
	Succeeded
	Failed
)

func (s State) String() string {
	switch s {
	case Waiting:
		return "Waiting"
	case Running:
		return "Running"
	case Succeeded:
		return "Succeeded"
	case Failed:
		return "Failed"
	default:
		return "Unknown"
	}
}

// Phase is the derived Pod phase.
type Phase int

const (
	PhasePending Phase = iota
	PhaseRunning
	PhaseSucceeded
	PhaseFailed
)

func (p Phase) String() string {
	switch p {
	case PhasePending:
		return "Pending"
	case PhaseRunning:
		return "Running"
	case PhaseSucceeded:
		return "Succeeded"
	case PhaseFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}

// RejectReason identifies why an operation was rejected.
type RejectReason int

const (
	RejectInvalidArgument RejectReason = iota
	RejectClockWentBack
	RejectStartNotWaiting
	RejectExitNotRunning
	RejectDeadlineExceeded
	RejectInitNotComplete
	RejectBackingOff
)

// RejectError is returned for every rejected Start/Exit and for bad now in
// Phase/Reason.
type RejectError struct {
	Reason RejectReason
	msg    string
}

func (e *RejectError) Error() string { return e.msg }

func asReject(err error) (*RejectError, bool) {
	r, ok := err.(*RejectError)
	return r, ok
}

// Config holds the immutable machine configuration.
type Config struct {
	InitContainers int
	AppContainers  int
	RestartPolicy  RestartPolicy
	BackoffBase    int64
	BackoffMax     int64
	BackoffReset   int64
	ActiveDeadline int64
	RestartBudget  int64
}

// ContainerSnapshot is an immutable view of one container.
type ContainerSnapshot struct {
	State   State
	Fails   int
	Next    int64
	Started bool
}

type container struct {
	state     State
	fails     int
	next      int64
	started   bool
	startedAt int64
}

// Pod is the concurrent-safe state machine.
type Pod struct {
	mu       sync.Mutex
	cfg      Config
	cs       []container
	restarts int64
	t0       int64
	hasT0    bool
	maxNow   int64
}
