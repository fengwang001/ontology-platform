// Package pod implements a deterministic Pod phase and restart-backoff state
// machine with a restart budget and an active deadline.
//
// A Pod runs I init containers strictly in order, followed by A app
// containers that may run in parallel. Container exits are routed by the
// restart policy and a shared restart budget; restarts are scheduled with
// an exponential backoff that resets after a sufficiently long run.
package pod

import (
	"errors"
	"fmt"
	"math"
	"sync"
)

// Policy is the Pod-level restart policy.
type Policy int

const (
	Always Policy = iota
	OnFailure
	Never
)

func (p Policy) String() string {
	switch p {
	case Always:
		return "Always"
	case OnFailure:
		return "OnFailure"
	case Never:
		return "Never"
	}
	return "Unknown"
}

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
	}
	return "Unknown"
}

// Terminal reports whether the state is a terminal state.
func (s State) Terminal() bool { return s == Succeeded || s == Failed }

// Phase is the aggregated Pod phase.
type Phase int

const (
	Pending Phase = iota
	PhaseRunning
	PhaseSucceeded
	PhaseFailed
)

func (p Phase) String() string {
	switch p {
	case Pending:
		return "Pending"
	case PhaseRunning:
		return "Running"
	case PhaseSucceeded:
		return "Succeeded"
	case PhaseFailed:
		return "Failed"
	}
	return "Unknown"
}

// RejectReason identifies why an operation was rejected.
type RejectReason int

const (
	// RejectInvalidArgument: container index out of range or now < 0.
	RejectInvalidArgument RejectReason = iota
	// RejectClockRegression: now is below the maximum now accepted so far.
	RejectClockRegression
	// RejectNotWaiting: Start on a container that is not Waiting.
	RejectNotWaiting
	// RejectNotRunning: Exit on a container that is not Running.
	RejectNotRunning
	// RejectDeadlineExceeded: Start after the active deadline has elapsed.
	RejectDeadlineExceeded
	// RejectInitNotComplete: Start blocked by unfinished init containers.
	RejectInitNotComplete
	// RejectBackoff: Start before the container's next allowed start time.
	RejectBackoff
)

func (r RejectReason) String() string {
	switch r {
	case RejectInvalidArgument:
		return "InvalidArgument"
	case RejectClockRegression:
		return "ClockRegression"
	case RejectNotWaiting:
		return "NotWaiting"
	case RejectNotRunning:
		return "NotRunning"
	case RejectDeadlineExceeded:
		return "DeadlineExceeded"
	case RejectInitNotComplete:
		return "InitNotComplete"
	case RejectBackoff:
		return "Backoff"
	}
	return "Unknown"
}

// Sentinel errors matching each RejectReason, for use with errors.Is.
var (
	ErrInvalidArgument  = errors.New("pod: invalid argument")
	ErrClockRegression  = errors.New("pod: clock regression")
	ErrNotWaiting       = errors.New("pod: container is not Waiting")
	ErrNotRunning       = errors.New("pod: container is not Running")
	ErrDeadlineExceeded = errors.New("pod: active deadline exceeded")
	ErrInitNotComplete  = errors.New("pod: init containers not complete")
	ErrBackoff          = errors.New("pod: container is backing off")
	ErrInvalidConfig    = errors.New("pod: invalid configuration")
)

// Error describes a rejected operation.
type Error struct {
	Op        string
	Container int
	Reason    RejectReason
}

func (e *Error) Error() string {
	return fmt.Sprintf("pod: %s container %d rejected: %s", e.Op, e.Container, e.Reason)
}

// Is maps the rejection to its sentinel error.
func (e *Error) Is(target error) bool {
	return target == sentinelFor(e.Reason)
}

func sentinelFor(r RejectReason) error {
	switch r {
	case RejectInvalidArgument:
		return ErrInvalidArgument
	case RejectClockRegression:
		return ErrClockRegression
	case RejectNotWaiting:
		return ErrNotWaiting
	case RejectNotRunning:
		return ErrNotRunning
	case RejectDeadlineExceeded:
		return ErrDeadlineExceeded
	case RejectInitNotComplete:
		return ErrInitNotComplete
	case RejectBackoff:
		return ErrBackoff
	}
	return nil
}

// Config holds the Pod construction parameters.
type Config struct {
	InitContainers int    // I: 0..16
	AppContainers  int    // A: 1..16
	Policy         Policy // Always, OnFailure or Never
	BackoffBase    int64  // B: 1..1e12, milliseconds
	BackoffMax     int64  // M: B..1e12, milliseconds
	BackoffReset   int64  // R: 1..1e12, milliseconds
	ActiveDeadline int64  // D: 0..1e12, milliseconds; 0 disables the deadline
	RestartBudget  int64  // X: 0..1e6; 0 means unlimited
}

const (
	maxContainers = 16
	maxBackoff    = int64(1_000_000_000_000)
	maxBudget     = int64(1_000_000)
)

// Validate checks the configuration bounds.
func (c Config) Validate() error {
	if c.InitContainers < 0 || c.InitContainers > maxContainers {
		return fmt.Errorf("%w: InitContainers=%d out of [0,16]", ErrInvalidConfig, c.InitContainers)
	}
	if c.AppContainers < 1 || c.AppContainers > maxContainers {
		return fmt.Errorf("%w: AppContainers=%d out of [1,16]", ErrInvalidConfig, c.AppContainers)
	}
	if c.Policy != Always && c.Policy != OnFailure && c.Policy != Never {
		return fmt.Errorf("%w: unknown Policy=%d", ErrInvalidConfig, c.Policy)
	}
	if c.BackoffBase < 1 || c.BackoffBase > maxBackoff {
		return fmt.Errorf("%w: BackoffBase=%d out of [1,1e12]", ErrInvalidConfig, c.BackoffBase)
	}
	if c.BackoffMax < c.BackoffBase || c.BackoffMax > maxBackoff {
		return fmt.Errorf("%w: BackoffMax=%d out of [BackoffBase,1e12]", ErrInvalidConfig, c.BackoffMax)
	}
	if c.BackoffReset < 1 || c.BackoffReset > maxBackoff {
		return fmt.Errorf("%w: BackoffReset=%d out of [1,1e12]", ErrInvalidConfig, c.BackoffReset)
	}
	if c.ActiveDeadline < 0 || c.ActiveDeadline > maxBackoff {
		return fmt.Errorf("%w: ActiveDeadline=%d out of [0,1e12]", ErrInvalidConfig, c.ActiveDeadline)
	}
	if c.RestartBudget < 0 || c.RestartBudget > maxBudget {
		return fmt.Errorf("%w: RestartBudget=%d out of [0,1e6]", ErrInvalidConfig, c.RestartBudget)
	}
	return nil
}

// container is the per-container state.
type container struct {
	state     State
	k         int64 // consecutive failure count
	next      int64 // next allowed start time
	started   bool  // ever started
	startedAt int64 // start time of the current run
}

// Pod is the state machine. All methods are safe for concurrent use; the
// result is equivalent to some serial execution order.
type Pod struct {
	mu       sync.Mutex
	cfg      Config
	cs       []container
	initN    int
	restarts int64 // global restart count shared by all containers
	t0       int64 // time of the first accepted Start
	t0Set    bool
	maxNow   int64 // max now seen by accepted Start/Exit; -1 when none
}

// New constructs a Pod after validating the configuration.
func New(cfg Config) (*Pod, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Pod{
		cfg:    cfg,
		cs:     make([]container, cfg.InitContainers+cfg.AppContainers),
		initN:  cfg.InitContainers,
		maxNow: -1,
	}, nil
}

// Config returns the Pod configuration.
func (p *Pod) Config() Config { return p.cfg }

// ContainerSnapshot is an immutable view of one container.
type ContainerSnapshot struct {
	State   State
	K       int64
	Next    int64
	Started bool
}

// Snapshot is an immutable view of the whole Pod.
type Snapshot struct {
	Containers []ContainerSnapshot
	Restarts   int64
	T0         int64
	T0Set      bool
}

// Snapshot returns a consistent copy of the full state.
func (p *Pod) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked()
}

func (p *Pod) snapshotLocked() Snapshot {
	snap := Snapshot{
		Containers: make([]ContainerSnapshot, len(p.cs)),
		Restarts:   p.restarts,
		T0:         p.t0,
		T0Set:      p.t0Set,
	}
	for i, c := range p.cs {
		snap.Containers[i] = ContainerSnapshot{
			State:   c.state,
			K:       c.k,
			Next:    c.next,
			Started: c.started,
		}
	}
	return snap
}

// Start attempts to start container c at time now.
//
// Rejections are reported in this order (first match wins): invalid
// argument, clock regression, not Waiting, deadline exceeded, init
// containers incomplete, backoff. A rejected Start changes nothing.
func (p *Pod) Start(c int, now int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c < 0 || c >= len(p.cs) || now < 0 {
		return &Error{Op: "Start", Container: c, Reason: RejectInvalidArgument}
	}
	if now < p.maxNow {
		return &Error{Op: "Start", Container: c, Reason: RejectClockRegression}
	}
	co := &p.cs[c]
	if co.state != Waiting {
		return &Error{Op: "Start", Container: c, Reason: RejectNotWaiting}
	}
	if p.deadlineExceededLocked(now) {
		return &Error{Op: "Start", Container: c, Reason: RejectDeadlineExceeded}
	}
	if !p.initGateOpenLocked(c) {
		return &Error{Op: "Start", Container: c, Reason: RejectInitNotComplete}
	}
	if now < co.next {
		return &Error{Op: "Start", Container: c, Reason: RejectBackoff}
	}
	co.state = Running
	co.startedAt = now
	co.started = true
	if !p.t0Set {
		p.t0 = now
		p.t0Set = true
	}
	p.maxNow = now
	return nil
}

// Exit records the exit of container c with the given exit code at time now.
//
// Rejections are reported in this order (first match wins): invalid
// argument, clock regression, not Running. A rejected Exit changes nothing.
func (p *Pod) Exit(c int, code int, now int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c < 0 || c >= len(p.cs) || now < 0 {
		return &Error{Op: "Exit", Container: c, Reason: RejectInvalidArgument}
	}
	if now < p.maxNow {
		return &Error{Op: "Exit", Container: c, Reason: RejectClockRegression}
	}
	co := &p.cs[c]
	if co.state != Running {
		return &Error{Op: "Exit", Container: c, Reason: RejectNotRunning}
	}
	ran := now - co.startedAt
	isInit := c < p.initN
	if restartOn(isInit, code, p.cfg.Policy) {
		if p.cfg.RestartBudget > 0 && p.restarts >= p.cfg.RestartBudget {
			// Budget exhausted: fall back to Never semantics without
			// touching k, next or restarts.
			co.state = terminalFor(code)
		} else {
			if ran >= p.cfg.BackoffReset {
				co.k = 0
			}
			delay := backoffDelay(p.cfg.BackoffBase, p.cfg.BackoffMax, co.k)
			co.next = satAdd(now, delay)
			co.k++
			p.restarts++
			co.state = Waiting
		}
	} else {
		co.state = terminalFor(code)
	}
	p.maxNow = now
	return nil
}

// Phase derives the Pod phase at time now.
//
// Only now < 0 is rejected (invalid argument); no clock-regression check.
func (p *Pod) Phase(now int64) (Phase, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < 0 {
		return Pending, &Error{Op: "Phase", Container: -1, Reason: RejectInvalidArgument}
	}
	return p.phaseLocked(now), nil
}

// Reason returns the human-readable reason string for container c at now.
//
// Only now < 0 and an out-of-range container index are rejected (invalid
// argument); no clock-regression check.
func (p *Pod) Reason(c int, now int64) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c < 0 || c >= len(p.cs) || now < 0 {
		return "", &Error{Op: "Reason", Container: c, Reason: RejectInvalidArgument}
	}
	co := &p.cs[c]
	if !co.state.Terminal() && p.deadlineExceededLocked(now) {
		return "DeadlineExceeded", nil
	}
	if co.state == Waiting && now < co.next {
		return "CrashLoopBackOff", nil
	}
	return co.state.String(), nil
}

// deadlineExceededLocked reports whether the active deadline has elapsed.
func (p *Pod) deadlineExceededLocked(now int64) bool {
	return p.cfg.ActiveDeadline > 0 && p.t0Set && now-p.t0 >= p.cfg.ActiveDeadline
}

// initGateOpenLocked reports whether the init-container gating allows
// container c to start: init container i needs all previous init containers
// Succeeded; app containers need all init containers Succeeded.
func (p *Pod) initGateOpenLocked(c int) bool {
	limit := p.initN
	if c < p.initN {
		limit = c
	}
	for i := 0; i < limit; i++ {
		if p.cs[i].state != Succeeded {
			return false
		}
	}
	return true
}

// phaseLocked derives the Pod phase. The check order is fixed: deadline,
// failed init container, all app containers terminal, pending gates.
func (p *Pod) phaseLocked(now int64) Phase {
	if p.deadlineExceededLocked(now) {
		return PhaseFailed
	}
	for i := 0; i < p.initN; i++ {
		if p.cs[i].state == Failed {
			return PhaseFailed
		}
	}
	allAppTerminal := true
	anyAppFailed := false
	anyAppStarted := false
	for i := p.initN; i < len(p.cs); i++ {
		switch {
		case !p.cs[i].state.Terminal():
			allAppTerminal = false
		case p.cs[i].state == Failed:
			anyAppFailed = true
		}
		if p.cs[i].started {
			anyAppStarted = true
		}
	}
	if allAppTerminal {
		if anyAppFailed {
			return PhaseFailed
		}
		return PhaseSucceeded
	}
	for i := 0; i < p.initN; i++ {
		if p.cs[i].state != Succeeded {
			return Pending
		}
	}
	if !anyAppStarted {
		return Pending
	}
	return PhaseRunning
}

// restartOn decides whether an exit routes into a restart under the policy,
// before the restart budget is consulted.
func restartOn(isInit bool, code int, pol Policy) bool {
	if isInit {
		if code == 0 {
			return false
		}
		return pol == Always || pol == OnFailure
	}
	if code == 0 {
		return pol == Always
	}
	return pol == Always || pol == OnFailure
}

// terminalFor maps an exit code to its terminal state when no restart
// happens: code 0 means Succeeded, anything else means Failed.
func terminalFor(code int) State {
	if code == 0 {
		return Succeeded
	}
	return Failed
}

// backoffDelay computes min(B*2^k, M) without overflowing: B*2^k <= M holds
// exactly when 2^k <= M/B (integer division), so the shift is only ever
// applied when the result is known to fit.
func backoffDelay(base, max, k int64) int64 {
	if k >= 63 || (int64(1)<<uint(k)) > max/base {
		return max
	}
	return base << uint(k)
}

// satAdd adds a non-negative delay to a timestamp, saturating on overflow.
func satAdd(now, delay int64) int64 {
	if delay > math.MaxInt64-now {
		return math.MaxInt64
	}
	return now + delay
}
