// Package checkpoint implements a checkpoint coordinator that drives
// triggering, acknowledgement, timeout, absorption, retention and recovery
// of checkpoints for a fixed number of tasks.
package checkpoint

import (
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// Status is the lifecycle state of a checkpoint.
type Status int

const (
	StatusInflight Status = iota
	StatusCompleted
	StatusAborted
)

// AbortReason explains why an inflight checkpoint was aborted.
type AbortReason int

const (
	AbortNone AbortReason = iota
	AbortTimeout
	AbortSwallowed
	AbortRecovery
	AbortFailed
)

// Config configures a Coordinator.
//
//   - Tasks N: exactly N distinct task acknowledgements complete a checkpoint.
//   - MaxInflight: triggering is rejected once this many checkpoints are inflight.
//   - MinInterval: a trigger must be at least this far after the latest completion.
//   - Timeout: an inflight checkpoint aborts once clock >= TriggeredAt + Timeout.
//   - ToleratedTimeouts: the coordinator fails when consecutive timeouts exceed it.
//   - Retention R: only the R largest completed checkpoint ids stay recoverable.
//   - Logger: optional structured logger; slog.Default() is used when nil.
//
// Clock is a virtual, monotonically non-decreasing duration measured from zero.
type Config struct {
	Tasks             int
	MaxInflight       int
	MinInterval       time.Duration
	Timeout           time.Duration
	ToleratedTimeouts int
	Retention         int
	Logger            *slog.Logger
}

// CheckpointView is an immutable view of a checkpoint.
type CheckpointView struct {
	ID          uint64
	Status      Status
	TriggeredAt time.Duration
	AckedTasks  []int
	AbortReason AbortReason
	EndedAt     time.Duration
}

// Snapshot is an immutable view of the whole coordinator.
type Snapshot struct {
	Clock               time.Duration
	Failed              bool
	NextID              uint64
	ConsecutiveTimeouts int
	InflightIDs         []uint64
	RetainedIDs         []uint64
	Checkpoints         map[uint64]CheckpointView
}

// Sentinel errors, reported in a documented fixed priority order.
var (
	ErrClockBackward     = errors.New("checkpoint: clock must not go backward")
	ErrFailed            = errors.New("checkpoint: coordinator has failed")
	ErrConcurrencyFull   = errors.New("checkpoint: inflight concurrency limit reached")
	ErrIntervalTooShort  = errors.New("checkpoint: minimum interval since last completion not elapsed")
	ErrTaskOutOfRange    = errors.New("checkpoint: task id out of range")
	ErrUnknownCheckpoint = errors.New("checkpoint: checkpoint id does not exist")
	ErrCheckpointEnded   = errors.New("checkpoint: checkpoint already ended")
	ErrDuplicateAck      = errors.New("checkpoint: task already acknowledged this checkpoint")
	ErrNoCheckpoint      = errors.New("checkpoint: no completed checkpoint retained for recovery")
	ErrInvalidConfig     = errors.New("checkpoint: invalid configuration")
)

// checkpoint is the mutable internal record guarded by Coordinator.mu.
type checkpoint struct {
	id          uint64
	status      Status
	triggeredAt time.Duration
	acks        map[int]struct{}
	abort       AbortReason
	endedAt     time.Duration
}

// Coordinator drives checkpoints. The zero value is not usable; use New.
//
// Every method is safe for concurrent use. All operations linearize on a
// single mutex, so interleaved concurrent calls observe exactly the same
// states and results as the operations replayed serially in that order.
type Coordinator struct {
	mu  sync.Mutex
	cfg Config
	log *slog.Logger

	clock  time.Duration
	failed bool
	nextID uint64
	consec int

	// lastCompletion is nil until the first checkpoint completes; afterwards it
	// is the clock of the most recent completion (swallows do not move it).
	lastCompletion *time.Duration

	// inflight keeps inflight ids ascending; retained keeps retained completed
	// ids ascending and is capped at Retention.
	inflight []uint64
	retained []uint64

	// store keeps every checkpoint ever created, so ended checkpoints can be
	// distinguished from unknown ids even after retention eviction.
	store map[uint64]*checkpoint
}

// New creates a coordinator. It returns an error for invalid configuration.
func New(cfg Config) (*Coordinator, error) {
	switch {
	case cfg.Tasks < 1:
		return nil, errors.Join(ErrInvalidConfig, errors.New("Tasks must be >= 1"))
	case cfg.MaxInflight < 1:
		return nil, errors.Join(ErrInvalidConfig, errors.New("MaxInflight must be >= 1"))
	case cfg.MinInterval < 0:
		return nil, errors.Join(ErrInvalidConfig, errors.New("MinInterval must be >= 0"))
	case cfg.Timeout < 0:
		return nil, errors.Join(ErrInvalidConfig, errors.New("Timeout must be >= 0"))
	case cfg.ToleratedTimeouts < 0:
		return nil, errors.Join(ErrInvalidConfig, errors.New("ToleratedTimeouts must be >= 0"))
	case cfg.Retention < 1:
		return nil, errors.Join(ErrInvalidConfig, errors.New("Retention must be >= 1"))
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Coordinator{
		cfg:    cfg,
		log:    log,
		nextID: 1,
		store:  make(map[uint64]*checkpoint),
	}, nil
}

// Trigger allocates a new checkpoint at the current clock.
// The returned id is assigned only on success; rejected triggers consume no id.
func (c *Coordinator) Trigger() (id uint64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Rejection priority: failed > concurrency full > interval too short.
	switch {
	case c.failed:
		c.log.Info("trigger rejected",
			slog.String("op", "trigger"),
			slog.Duration("clock", c.clock),
			slog.Any("output", ErrFailed),
			slog.String("basis", "coordinator failed; all triggers rejected"))
		return 0, ErrFailed
	case len(c.inflight) >= c.cfg.MaxInflight:
		c.log.Info("trigger rejected",
			slog.String("op", "trigger"),
			slog.Duration("clock", c.clock),
			slog.Int("inflight", len(c.inflight)),
			slog.Int("limit", c.cfg.MaxInflight),
			slog.Any("output", ErrConcurrencyFull),
			slog.String("basis", "inflight count already at concurrency limit"))
		return 0, ErrConcurrencyFull
	case c.lastCompletion != nil && c.clock-*c.lastCompletion < c.cfg.MinInterval:
		c.log.Info("trigger rejected",
			slog.String("op", "trigger"),
			slog.Duration("clock", c.clock),
			slog.Duration("last_completion", *c.lastCompletion),
			slog.Duration("min_interval", c.cfg.MinInterval),
			slog.Any("output", ErrIntervalTooShort),
			slog.String("basis", "clock-lastCompletion < MinInterval"))
		return 0, ErrIntervalTooShort
	}

	id = c.nextID
	c.nextID++
	c.store[id] = &checkpoint{
		id:          id,
		status:      StatusInflight,
		triggeredAt: c.clock,
		acks:        make(map[int]struct{}),
	}
	c.inflight = append(c.inflight, id)
	c.log.Info("trigger accepted",
		slog.String("op", "trigger"),
		slog.Duration("clock", c.clock),
		slog.Uint64("output_id", id),
		slog.Int("inflight", len(c.inflight)),
		slog.String("basis", "healthy, under limit, interval satisfied"))
	return id, nil
}

// Ack records an acknowledgement from task for checkpoint id.
// Tasks are numbered 0..N-1.
func (c *Coordinator) Ack(id uint64, task int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Rejection priority: task out of range > unknown id > ended > duplicate.
	if task < 0 || task >= c.cfg.Tasks {
		c.log.Info("ack rejected",
			slog.String("op", "ack"),
			slog.Uint64("id", id),
			slog.Int("task", task),
			slog.Any("output", ErrTaskOutOfRange),
			slog.String("basis", "task not in [0,N)"))
		return ErrTaskOutOfRange
	}
	cp, ok := c.store[id]
	if !ok {
		c.log.Info("ack rejected",
			slog.String("op", "ack"),
			slog.Uint64("id", id),
			slog.Int("task", task),
			slog.Any("output", ErrUnknownCheckpoint),
			slog.String("basis", "no checkpoint ever carried this id; rejected triggers consume no id"))
		return ErrUnknownCheckpoint
	}
	if cp.status != StatusInflight {
		c.log.Info("ack rejected",
			slog.String("op", "ack"),
			slog.Uint64("id", id),
			slog.Int("task", task),
			slog.Int("status", int(cp.status)),
			slog.Any("output", ErrCheckpointEnded),
			slog.String("basis", "checkpoint already completed or aborted"))
		return ErrCheckpointEnded
	}
	if _, dup := cp.acks[task]; dup {
		c.log.Info("ack rejected",
			slog.String("op", "ack"),
			slog.Uint64("id", id),
			slog.Int("task", task),
			slog.Any("output", ErrDuplicateAck),
			slog.String("basis", "task already acknowledged this checkpoint"))
		return ErrDuplicateAck
	}

	cp.acks[task] = struct{}{}

	if len(cp.acks) < c.cfg.Tasks {
		c.log.Info("ack recorded",
			slog.String("op", "ack"),
			slog.Uint64("id", id),
			slog.Int("task", task),
			slog.Int("acked", len(cp.acks)),
			slog.Int("tasks", c.cfg.Tasks),
			slog.String("basis", "distinct task, checkpoint still inflight"))
		return nil
	}

	// All N distinct tasks acknowledged: completion. The streak resets, the
	// completion clock anchors the minimum interval, every smaller inflight
	// checkpoint is swallowed, and the retention set keeps the R largest ids.
	cp.status = StatusCompleted
	cp.endedAt = c.clock
	c.consec = 0
	completionAt := c.clock
	c.lastCompletion = &completionAt

	swallowed := make([]uint64, 0)
	remaining := c.inflight[:0]
	for _, inflightID := range c.inflight {
		switch {
		case inflightID == id:
			// completed: dropped from inflight
		case inflightID < id:
			other := c.store[inflightID]
			other.status = StatusAborted
			other.abort = AbortSwallowed
			other.endedAt = c.clock
			swallowed = append(swallowed, inflightID)
		default:
			remaining = append(remaining, inflightID)
		}
	}
	c.inflight = remaining

	c.retained = append(c.retained, id)
	slices.Sort(c.retained)
	evicted := 0
	if len(c.retained) > c.cfg.Retention {
		evicted = len(c.retained) - c.cfg.Retention
		// Evicted ids stay in store for ended-vs-unknown error distinction.
		c.retained = c.retained[evicted:]
	}
	c.log.Info("ack completed checkpoint",
		slog.String("op", "ack"),
		slog.Uint64("id", id),
		slog.Int("task", task),
		slog.Any("swallowed_ids", swallowed),
		slog.Any("retained_ids", c.retained),
		slog.Int("retention_evicted", evicted),
		slog.Int("consecutive_timeouts", c.consec),
		slog.String("basis", "N distinct tasks acked; smaller inflight swallowed; streak cleared"))
	return nil
}

// Advance moves the clock forward and processes due timeouts in id order.
// Equal clocks are allowed (the clock never goes backward); only Advance can
// move the clock or decide timeouts.
func (c *Coordinator) Advance(now time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.clock {
		c.log.Info("advance rejected",
			slog.String("op", "advance"),
			slog.Duration("input_now", now),
			slog.Duration("clock", c.clock),
			slog.Any("output", ErrClockBackward),
			slog.String("basis", "new clock is earlier than current clock; state unchanged"))
		return ErrClockBackward
	}
	c.clock = now

	// Freeze the ascending id list first: within one advance checkpoints are
	// handled strictly in ascending id order, one by one.
	pending := slices.Clone(c.inflight)
	timedOut := make([]uint64, 0)
	failAborted := make([]uint64, 0)
	for index, inflightID := range pending {
		cp := c.store[inflightID]
		if cp.status != StatusInflight {
			continue
		}
		if now < cp.triggeredAt+c.cfg.Timeout {
			continue
		}
		cp.status = StatusAborted
		cp.abort = AbortTimeout
		cp.endedAt = now
		timedOut = append(timedOut, inflightID)
		c.consec++
		if c.consec > c.cfg.ToleratedTimeouts {
			// Coordinator fails: every other inflight checkpoint aborts for
			// failure; those aborts do not count toward the timeout streak.
			c.failed = true
			for _, restID := range pending[index+1:] {
				rest := c.store[restID]
				if rest.status != StatusInflight {
					continue
				}
				rest.status = StatusAborted
				rest.abort = AbortFailed
				rest.endedAt = now
				failAborted = append(failAborted, restID)
			}
			break
		}
	}
	if len(timedOut) > 0 || len(failAborted) > 0 {
		c.inflight = c.inflight[:0]
		for _, inflightID := range pending {
			if c.store[inflightID].status == StatusInflight {
				c.inflight = append(c.inflight, inflightID)
			}
		}
	}
	c.log.Info("advance applied",
		slog.String("op", "advance"),
		slog.Duration("input_now", now),
		slog.Any("timeout_ids", timedOut),
		slog.Any("failure_abort_ids", failAborted),
		slog.Bool("failed", c.failed),
		slog.Int("consecutive_timeouts", c.consec),
		slog.Int("tolerated", c.cfg.ToleratedTimeouts),
		slog.Any("inflight_ids", c.inflight),
		slog.String("basis", "due timeouts processed in id order; streak failure aborts the rest without counting"))
	return nil
}

// Recover returns the retained checkpoint with the largest id, aborts all
// inflight checkpoints, clears the timeout streak and clears failure.
func (c *Coordinator) Recover() (id uint64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.retained) == 0 {
		c.log.Info("recover rejected",
			slog.String("op", "recover"),
			slog.Any("output", ErrNoCheckpoint),
			slog.String("basis", "retention set empty; state unchanged"))
		return 0, ErrNoCheckpoint
	}

	id = c.retained[len(c.retained)-1]
	aborted := slices.Clone(c.inflight)
	for _, inflightID := range aborted {
		cp := c.store[inflightID]
		cp.status = StatusAborted
		cp.abort = AbortRecovery
		cp.endedAt = c.clock
	}
	c.inflight = nil
	wasFailed := c.failed
	c.failed = false
	c.consec = 0
	c.log.Info("recover applied",
		slog.String("op", "recover"),
		slog.Uint64("output_id", id),
		slog.Any("aborted_ids", aborted),
		slog.Bool("was_failed", wasFailed),
		slog.Int("consecutive_timeouts", c.consec),
		slog.Any("retained_ids", c.retained),
		slog.String("basis", "largest retained id returned; inflight aborted without counting; failure and streak cleared"))
	return id, nil
}

// Query returns an immutable snapshot of the coordinator state.
func (c *Coordinator) Query() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	snap := Snapshot{
		Clock:               c.clock,
		Failed:              c.failed,
		NextID:              c.nextID,
		ConsecutiveTimeouts: c.consec,
		InflightIDs:         slices.Clone(c.inflight),
		RetainedIDs:         slices.Clone(c.retained),
		Checkpoints:         make(map[uint64]CheckpointView, len(c.store)),
	}
	for id, cp := range c.store {
		view := CheckpointView{
			ID:          cp.id,
			Status:      cp.status,
			TriggeredAt: cp.triggeredAt,
			AckedTasks:  make([]int, 0, len(cp.acks)),
			AbortReason: cp.abort,
			EndedAt:     cp.endedAt,
		}
		for task := range cp.acks {
			view.AckedTasks = append(view.AckedTasks, task)
		}
		slices.Sort(view.AckedTasks)
		snap.Checkpoints[id] = view
	}
	return snap
}
