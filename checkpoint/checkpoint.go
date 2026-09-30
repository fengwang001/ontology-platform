// Package checkpoint implements a checkpoint coordinator: it drives
// triggering, confirmation, timeout, subsumption, retention and restore
// for a fixed number of tasks. All methods are safe for concurrent use;
// internally a mutex serializes every event so the result always matches
// a per-event serial execution.
package checkpoint

import (
	"fmt"
	"log"
	"sort"
	"sync"
)

// Reason is a distinguishable rejection reason.
type Reason string

const (
	ReasonClockBackward     Reason = "clock_backward"
	ReasonCoordinatorFailed Reason = "coordinator_failed"
	ReasonConcurrencyFull   Reason = "concurrency_full"
	ReasonIntervalTooShort  Reason = "interval_too_short"
	ReasonTaskOutOfRange    Reason = "task_out_of_range"
	ReasonNotFound          Reason = "not_found"
	ReasonAlreadyEnded      Reason = "already_ended"
	ReasonDuplicateConfirm  Reason = "duplicate_confirm"
	ReasonNoCheckpoint      Reason = "no_checkpoint"
)

// Error describes an operation rejected as a whole; a rejected operation
// does not change any state.
type Error struct {
	Op     string
	Reason Reason
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s rejected: %s (%s)", e.Op, e.Reason, e.Detail)
}

// AbortReason is why a checkpoint was aborted.
type AbortReason string

const (
	AbortTimeout  AbortReason = "timeout"
	AbortSubsumed AbortReason = "subsumed"
	AbortRestored AbortReason = "restored"
	AbortFailed   AbortReason = "failed"
)

// State is the checkpoint lifecycle state.
type State string

const (
	StateInProgress State = "in_progress"
	StateCompleted  State = "completed"
	StateAborted    State = "aborted"
)

// Config holds coordinator parameters.
type Config struct {
	Tasks                 int
	MaxConcurrent         int
	MinInterval           int64
	Timeout               int64
	MaxConsecutiveTimeout int
	Retention             int
}

// Checkpoint is a snapshot of a single checkpoint.
type Checkpoint struct {
	ID          uint64
	StartedAt   int64
	CompletedAt int64
	State       State
	AbortReason AbortReason
	Confirmed   []int
}

// Coordinator coordinates checkpoints. All methods may be called
// concurrently.
type Coordinator struct {
	mu     sync.Mutex
	cfg    Config
	logger *log.Logger

	clock               int64
	nextID              uint64
	failed              bool
	consecutiveTimeouts int
	lastCompletedAt     int64
	hasCompleted        bool

	checkpoints map[uint64]*checkpoint
	retained    []uint64
}

type checkpoint struct {
	id          uint64
	startedAt   int64
	completedAt int64
	state       State
	abortReason AbortReason
	confirmed   map[int]bool
}

// NewCoordinator creates a coordinator; invalid config returns an error.
func NewCoordinator(cfg Config, logger *log.Logger) (*Coordinator, error) {
	if logger == nil {
		logger = log.Default()
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	c := &Coordinator{
		cfg:         cfg,
		logger:      logger,
		nextID:      1,
		checkpoints: make(map[uint64]*checkpoint),
	}
	c.logf("init", "cfg=%+v", cfg)
	return c, nil
}

func (cfg Config) validate() error {
	switch {
	case cfg.Tasks <= 0:
		return fmt.Errorf("invalid config: Tasks must be > 0, got %d", cfg.Tasks)
	case cfg.MaxConcurrent <= 0:
		return fmt.Errorf("invalid config: MaxConcurrent must be > 0, got %d", cfg.MaxConcurrent)
	case cfg.MinInterval < 0:
		return fmt.Errorf("invalid config: MinInterval must be >= 0, got %d", cfg.MinInterval)
	case cfg.Timeout <= 0:
		return fmt.Errorf("invalid config: Timeout must be > 0, got %d", cfg.Timeout)
	case cfg.MaxConsecutiveTimeout < 0:
		return fmt.Errorf("invalid config: MaxConsecutiveTimeout must be >= 0, got %d", cfg.MaxConsecutiveTimeout)
	case cfg.Retention <= 0:
		return fmt.Errorf("invalid config: Retention must be > 0, got %d", cfg.Retention)
	}
	return nil
}

// Trigger allocates an increasing id at the current clock.
// Rejection priority: failed > concurrency full > interval too short.
// Rejected triggers do not consume an id.
func (c *Coordinator) Trigger() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.triggerLocked()
}

// Confirm records task's confirmation for checkpoint id.
// Rejection priority: task out of range > not found > already ended >
// duplicate confirm.
func (c *Coordinator) Confirm(id uint64, task int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.confirmLocked(id, task)
}

// Advance moves the clock to t and judges timeouts one by one in
// ascending id order within the advance. Moving the clock backwards is
// rejected as a whole.
func (c *Coordinator) Advance(t int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.advanceLocked(t)
}

// Restore returns the largest retained checkpoint id, aborts all
// in-progress checkpoints, resets the consecutive timeout counter and
// recovers a failed coordinator. Rejected when nothing is retained.
func (c *Coordinator) Restore() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.restoreLocked()
}

// Snapshot returns a consistent state snapshot; safe for concurrent use.
func (c *Coordinator) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

// Get returns the snapshot of checkpoint id; ok=false when absent.
func (c *Coordinator) Get(id uint64) (Checkpoint, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getLocked(id)
}

// Snapshot is a consistent view of the coordinator.
type Snapshot struct {
	Clock               int64
	NextID              uint64
	Failed              bool
	ConsecutiveTimeouts int
	InProgress          []uint64
	Retained            []uint64
}

func (c *Coordinator) logf(op, format string, args ...any) {
	c.logger.Printf("op=%s %s", op, fmt.Sprintf(format, args...))
}

func sortedKeys(m map[uint64]*checkpoint) []uint64 {
	ids := make([]uint64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
