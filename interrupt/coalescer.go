package interrupt

import (
	"errors"
	"strconv"
	"sync"
)

var (
	ErrNegativeGap       = errors.New("interrupt: gap must not be negative")
	ErrThresholdTooSmall = errors.New("interrupt: threshold must be at least 1")
	ErrTimeBeforeLast    = errors.New("interrupt: operation time must not be before the previous operation")
	ErrInvalidEventCount = errors.New("interrupt: event count must be at least 1")
	ErrNoPendingAck      = errors.New("interrupt: ack requires a pending interrupt")
)

type Record struct {
	Time  int64
	Count int64
}

type OpResult struct {
	Op      string
	Time    int64
	Count   int64
	Fired   bool
	Record  Record
	Pending int64
	Waiting bool
	Masked  bool
	Reasons []string
}

// State is a point-in-time copy of the coalescer's observable state.
type State struct {
	Pending        int64
	LastTrigger    int64
	HasTriggered   bool
	WaitingForAck  bool
	Masked         bool
	LastOpTime     int64
	HasPreviousOp  bool
	SuccessfulAcks int64
}

type Coalescer struct {
	mu        sync.Mutex
	gap       int64
	threshold int64

	pending        int64
	last           int64
	hasLast        bool
	waiting        bool
	masked         bool
	lastOp         int64
	hasPreviousOp  bool
	successfulAcks int64
	records        []Record
}

// New creates a coalescer with the minimum trigger gap and event threshold.
func New(gap, threshold int64) (*Coalescer, error) {
	if gap < 0 {
		return nil, ErrNegativeGap
	}
	if threshold < 1 {
		return nil, ErrThresholdTooSmall
	}

	return &Coalescer{
		gap:       gap,
		threshold: threshold,
	}, nil
}

// Event adds n events and then performs the normal trigger checks.
func (c *Coalescer) Event(t, n int64) (OpResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.validateTime(t); err != nil {
		return OpResult{Op: "Event", Time: t, Count: n}, err
	}
	if n < 1 {
		return OpResult{Op: "Event", Time: t, Count: n}, ErrInvalidEventCount
	}

	result := OpResult{Op: "Event", Time: t, Count: n}
	c.catchUp(t, &result)
	c.pending += n
	result.Reasons = append(result.Reasons, "step 2: added event count to pending")
	c.tryFire(t, "step 3", &result)
	c.acceptOperation(t, &result)
	return result, nil
}

// Tick performs only the catch-up and current-time trigger checks.
func (c *Coalescer) Tick(t int64) (OpResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.validateTime(t); err != nil {
		return OpResult{Op: "Tick", Time: t}, err
	}

	result := OpResult{Op: "Tick", Time: t}
	c.catchUp(t, &result)
	result.Reasons = append(result.Reasons, "step 2: tick has no state change")
	c.tryFire(t, "step 3", &result)
	c.acceptOperation(t, &result)
	return result, nil
}

// Ack acknowledges the interrupt currently waiting for confirmation.
func (c *Coalescer) Ack(t int64) (OpResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.validateTime(t); err != nil {
		return OpResult{Op: "Ack", Time: t}, err
	}
	if !c.waiting {
		return OpResult{Op: "Ack", Time: t}, ErrNoPendingAck
	}

	result := OpResult{Op: "Ack", Time: t}
	c.catchUp(t, &result)
	c.waiting = false
	c.successfulAcks++
	result.Reasons = append(result.Reasons, "step 2: cleared waiting-for-ack")
	c.tryFire(t, "step 3", &result)
	c.acceptOperation(t, &result)
	return result, nil
}

// Mask suppresses triggers after the catch-up check for this operation.
func (c *Coalescer) Mask(t int64) (OpResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.validateTime(t); err != nil {
		return OpResult{Op: "Mask", Time: t}, err
	}

	result := OpResult{Op: "Mask", Time: t}
	c.catchUp(t, &result)
	c.masked = true
	result.Reasons = append(result.Reasons, "step 2: mask enabled")
	c.tryFire(t, "step 3", &result)
	c.acceptOperation(t, &result)
	return result, nil
}

// Unmask re-enables triggers and then performs the current-time check.
func (c *Coalescer) Unmask(t int64) (OpResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.validateTime(t); err != nil {
		return OpResult{Op: "Unmask", Time: t}, err
	}

	result := OpResult{Op: "Unmask", Time: t}
	c.catchUp(t, &result)
	c.masked = false
	result.Reasons = append(result.Reasons, "step 2: mask disabled")
	c.tryFire(t, "step 3", &result)
	c.acceptOperation(t, &result)
	return result, nil
}

// Records returns a copy of all interrupt records in trigger order.
func (c *Coalescer) Records() []Record {
	c.mu.Lock()
	defer c.mu.Unlock()

	records := make([]Record, len(c.records))
	copy(records, c.records)
	return records
}

// Snapshot returns a copy of the current state.
func (c *Coalescer) Snapshot() State {
	c.mu.Lock()
	defer c.mu.Unlock()

	return State{
		Pending:        c.pending,
		LastTrigger:    c.last,
		HasTriggered:   c.hasLast,
		WaitingForAck:  c.waiting,
		Masked:         c.masked,
		LastOpTime:     c.lastOp,
		HasPreviousOp:  c.hasPreviousOp,
		SuccessfulAcks: c.successfulAcks,
	}
}

func (c *Coalescer) validateTime(t int64) error {
	if c.hasPreviousOp && t < c.lastOp {
		return ErrTimeBeforeLast
	}
	return nil
}

func (c *Coalescer) catchUp(t int64, result *OpResult) {
	if !c.hasLast {
		result.Reasons = append(result.Reasons, "step 1: no previous trigger, catch-up skipped")
		return
	}

	allowedAt := c.last + c.gap
	switch {
	case c.pending == 0:
		result.Reasons = append(result.Reasons, "step 1: no pending events")
	case c.waiting:
		result.Reasons = append(result.Reasons, "step 1: waiting for ack")
	case c.masked:
		result.Reasons = append(result.Reasons, "step 1: interrupts masked")
	case c.pending >= c.threshold:
		result.Reasons = append(result.Reasons, "step 1: threshold pending, current-time check decides trigger time")
	case t < allowedAt:
		result.Reasons = append(result.Reasons, "step 1: allowed time has not arrived")
	default:
		c.fire(allowedAt, "step 1", result)
	}
}

func (c *Coalescer) tryFire(t int64, stage string, result *OpResult) {
	if c.pending == 0 {
		result.Reasons = append(result.Reasons, stage+": no pending events")
		return
	}
	if c.waiting {
		result.Reasons = append(result.Reasons, stage+": waiting for ack")
		return
	}
	if c.masked {
		result.Reasons = append(result.Reasons, stage+": interrupts masked")
		return
	}

	allowedAt := c.last + c.gap
	if !c.hasLast {
		if c.pending < c.threshold {
			result.Reasons = append(result.Reasons, stage+": below threshold and no previous trigger")
			return
		}
	} else if t < allowedAt && c.pending < c.threshold {
		result.Reasons = append(result.Reasons, stage+": before allowed time and below threshold")
		return
	}

	c.fire(t, stage, result)
}

func (c *Coalescer) fire(t int64, stage string, result *OpResult) {
	record := Record{Time: t, Count: c.pending}
	c.records = append(c.records, record)
	c.pending = 0
	c.last = t
	c.hasLast = true
	c.waiting = true

	result.Fired = true
	result.Record = record
	result.Reasons = append(result.Reasons, stage+": fired interrupt at time "+strconv.FormatInt(t, 10)+
		" with count "+strconv.FormatInt(record.Count, 10))
}

func (c *Coalescer) acceptOperation(t int64, result *OpResult) {
	c.lastOp = t
	c.hasPreviousOp = true

	result.Pending = c.pending
	result.Waiting = c.waiting
	result.Masked = c.masked
}
