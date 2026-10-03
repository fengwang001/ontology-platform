package ontology

import (
	"errors"
	"sync"
)

const (
	maxInterval     = 1_000_000_000
	maxTimerCount   = 64
	maxAdvanceWakes = 100_000
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrDuplicateID     = errors.New("duplicate timer id")
	ErrTimerNotFound   = errors.New("timer not found")
	ErrClockRollback   = errors.New("clock rollback")
	ErrTimerCapacity   = errors.New("timer capacity exceeded")
	ErrNoTimers        = errors.New("no timers")
)

type FiredEvent struct {
	ID   string
	Late uint64
	Skip uint64
}

type WakeResult struct {
	At    uint64
	Fired []FiredEvent
	Left  int
}

type Stats struct {
	WakeCount    uint64
	FiredCount   uint64
	LateCount    uint64
	SkippedCount uint64
}

type timer struct {
	id       string
	period   uint64
	slack    uint64
	next     uint64
	deadline uint64

	deadlineIndex int
	nextIndex     int
	readyIndex    int
}

type Coalescer struct {
	mu     sync.RWMutex
	gap    uint64
	batch  int
	timers map[string]*timer

	deadlines *indexedHeap
	nexts     *indexedHeap
	ready     *indexedHeap

	last    uint64
	hasLast bool

	stats Stats
}

func NewCoalescer(gap uint64, batch int) (*Coalescer, error) {
	if gap > maxInterval || batch < 1 || batch > maxTimerCount {
		return nil, ErrInvalidArgument
	}
	return &Coalescer{
		gap:       gap,
		batch:     batch,
		timers:    map[string]*timer{},
		deadlines: newDeadlineHeap(),
		nexts:     newNextHeap(),
		ready:     newReadyHeap(),
	}, nil
}

func (c *Coalescer) Add(id string, period, slack, next uint64) error {
	if len(id) == 0 || len(id) > 32 || period < 1 || period > maxInterval || slack >= period || next > 100_000_000_000_000 {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.timers[id]; exists {
		return ErrDuplicateID
	}
	if c.hasLast && next < c.last {
		return ErrClockRollback
	}
	if len(c.timers) >= maxTimerCount {
		return ErrTimerCapacity
	}
	t := &timer{
		id:            id,
		period:        period,
		slack:         slack,
		next:          next,
		deadline:      next + slack,
		deadlineIndex: -1,
		nextIndex:     -1,
		readyIndex:    -1,
	}
	c.timers[id] = t
	c.deadlines.push(t)
	if c.hasLast && next <= c.last {
		c.ready.push(t)
	} else {
		c.nexts.push(t)
	}
	return nil
}

func (c *Coalescer) Remove(id string) error {
	if len(id) == 0 || len(id) > 32 {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t, exists := c.timers[id]
	if !exists {
		return ErrTimerNotFound
	}
	c.deadlines.remove(t.deadlineIndex)
	c.nexts.remove(t.nextIndex)
	if t.readyIndex >= 0 {
		c.ready.remove(t.readyIndex)
	}
	delete(c.timers, id)
	return nil
}

func (c *Coalescer) Next() (uint64, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nextLocked()
}

func (c *Coalescer) Wake() (WakeResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wakeLocked()
}

func (c *Coalescer) AdvanceTo(t uint64) ([]WakeResult, error) {
	if t > 100_000_000_000_000 {
		return nil, ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hasLast && t < c.last {
		return nil, ErrClockRollback
	}
	if len(c.timers) == 0 {
		return nil, ErrNoTimers
	}
	results := make([]WakeResult, 0)
	for len(results) < maxAdvanceWakes {
		nextAt, err := c.nextLocked()
		if err != nil || nextAt > t {
			break
		}
		result, err := c.wakeLocked()
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (c *Coalescer) Stats() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stats
}

func (c *Coalescer) nextLocked() (uint64, error) {
	if c.deadlines.len() == 0 {
		return 0, ErrNoTimers
	}
	earliest := c.deadlines.peek().deadline
	if c.hasLast && c.last+c.gap > earliest {
		return c.last + c.gap, nil
	}
	return earliest, nil
}

func (c *Coalescer) wakeLocked() (WakeResult, error) {
	at, err := c.nextLocked()
	if err != nil {
		return WakeResult{}, err
	}

	for {
		t, ready := c.nexts.popNextIfAtOrBefore(at)
		if !ready {
			break
		}
		c.ready.push(t)
	}

	candidateCount := c.ready.len()
	fireCount := c.batch
	if fireCount > candidateCount {
		fireCount = candidateCount
	}

	result := WakeResult{
		At:    at,
		Fired: make([]FiredEvent, 0, fireCount),
		Left:  candidateCount - fireCount,
	}

	for i := 0; i < fireCount; i++ {
		t := c.ready.pop()
		c.deadlines.remove(t.deadlineIndex)

		var late uint64
		if at > t.deadline {
			late = at - t.deadline
		}
		skips := (at - t.next) / t.period

		result.Fired = append(result.Fired, FiredEvent{
			ID:   t.id,
			Late: late,
			Skip: skips,
		})

		t.next += (skips + 1) * t.period
		t.deadline = t.next + t.slack
		c.nexts.push(t)
		c.deadlines.push(t)

		c.stats.FiredCount++
		if late > 0 {
			c.stats.LateCount++
		}
		c.stats.SkippedCount += skips
	}

	c.last = at
	c.hasLast = true
	c.stats.WakeCount++
	return result, nil
}
