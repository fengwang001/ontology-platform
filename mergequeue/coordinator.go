package mergequeue

import (
	"errors"
	"sync"
)

var (
	ErrInvalidB      = errors.New("mergequeue: build window B must be >= 1")
	ErrInvalidCap    = errors.New("mergequeue: capacity Cap must be >= 1")
	ErrEmptyID       = errors.New("mergequeue: id must not be empty")
	ErrAlreadyMerged = errors.New("mergequeue: id has already been merged")
	ErrAlreadyQueued = errors.New("mergequeue: id is already in the queue")
	ErrQueueFull     = errors.New("mergequeue: queue is full")
	ErrIDNotQueued   = errors.New("mergequeue: id is not in the queue")
	ErrNotBuilding   = errors.New("mergequeue: item is outside the build window")
	ErrStaleEpoch    = errors.New("mergequeue: report epoch does not match current epoch")
)

type ItemStatus struct {
	Position int
	Epoch    int
	Building bool
}

// Outcome is the result of a successful Report or Dequeue operation.
// Merged lists ids merged by the operation in queue (position) order;
// Rejected is the id that failed its build or was withdrawn;
// EpochBumped lists the items strictly behind Rejected whose epoch
// increased, in queue order.
type Outcome struct {
	Merged      []string
	Rejected    string
	EpochBumped []string
}

type entry struct {
	id    string
	epoch int
}

type Coordinator struct {
	mu      sync.Mutex
	b       int
	cap     int
	queue   []entry
	present map[string]bool
	merged  map[string]bool
	history []string
}

func NewCoordinator(buildWindow, capacity int) (*Coordinator, error) {
	if buildWindow < 1 {
		return nil, ErrInvalidB
	}
	if capacity < 1 {
		return nil, ErrInvalidCap
	}
	return &Coordinator{
		b:       buildWindow,
		cap:     capacity,
		present: make(map[string]bool),
		merged:  make(map[string]bool),
	}, nil
}

// Enqueue appends id to the tail of the queue with epoch 1.
// It is rejected (without any state change) when the id is empty,
// has already been merged, is already queued, or the queue is full,
// checked in that order.
func (c *Coordinator) Enqueue(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == "" {
		return ErrEmptyID
	}
	if c.merged[id] {
		return ErrAlreadyMerged
	}
	if c.present[id] {
		return ErrAlreadyQueued
	}
	if len(c.queue) >= c.cap {
		return ErrQueueFull
	}
	c.queue = append(c.queue, entry{id: id, epoch: 1})
	c.present[id] = true
	return nil
}

// Report reports a build result for id at the given epoch.
// On pass, positions 1..p (p = id's position) merge together in
// position order; surviving items shift forward without epoch changes.
// On failure, id is dropped and every item strictly behind it gets its
// epoch incremented. Rejection checks run in the order: id not queued,
// position outside the build window, stale epoch.
func (c *Coordinator) Report(id string, epoch int, passed bool) (Outcome, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pos := c.positionLocked(id)
	if pos < 0 {
		return Outcome{}, ErrIDNotQueued
	}
	if pos > c.b {
		return Outcome{}, ErrNotBuilding
	}
	if c.queue[pos-1].epoch != epoch {
		return Outcome{}, ErrStaleEpoch
	}
	if passed {
		return c.mergePrefixLocked(pos), nil
	}
	return c.removeLocked(pos), nil
}

// Dequeue withdraws id from the queue; the effect on items behind it is
// identical to a failed report (their epochs are incremented).
func (c *Coordinator) Dequeue(id string) (Outcome, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pos := c.positionLocked(id)
	if pos < 0 {
		return Outcome{}, ErrIDNotQueued
	}
	return c.removeLocked(pos), nil
}

// Merged returns the merged ids in historical merge order.
func (c *Coordinator) Merged() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.history...)
}

// Status returns the current position, epoch and building flag of every
// queued id.
func (c *Coordinator) Status() map[string]ItemStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := make(map[string]ItemStatus, len(c.queue))
	for i, e := range c.queue {
		status[e.id] = ItemStatus{
			Position: i + 1,
			Epoch:    e.epoch,
			Building: i+1 <= c.b,
		}
	}
	return status
}

func (c *Coordinator) positionLocked(id string) int {
	for i, e := range c.queue {
		if e.id == id {
			return i + 1
		}
	}
	return -1
}

func (c *Coordinator) mergePrefixLocked(pos int) Outcome {
	ids := make([]string, pos)
	for i := 0; i < pos; i++ {
		ids[i] = c.queue[i].id
	}
	for _, id := range ids {
		delete(c.present, id)
		c.merged[id] = true
	}
	c.history = append(c.history, ids...)
	c.queue = c.queue[pos:]
	return Outcome{Merged: ids}
}

func (c *Coordinator) removeLocked(pos int) Outcome {
	rejected := c.queue[pos-1].id
	delete(c.present, rejected)
	bumped := make([]string, 0, len(c.queue)-pos)
	for i := pos; i < len(c.queue); i++ {
		c.queue[i].epoch++
		bumped = append(bumped, c.queue[i].id)
	}
	c.queue = append(c.queue[:pos-1], c.queue[pos:]...)
	return Outcome{Rejected: rejected, EpochBumped: bumped}
}
