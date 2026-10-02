package ontology

import (
	"errors"
	"sync"
)

var (
	ErrInvalidArgument  = errors.New("ontology: invalid argument")
	ErrClockRolledBack  = errors.New("ontology: clock rolled back")
	ErrCapacityExceeded = errors.New("ontology: capacity exceeded")
	ErrKeyNotFound      = errors.New("ontology: key not found")
)

type idleEntry struct {
	key     string
	created int64
	timer   int64
	index   int
}

type IdleCleaner struct {
	mu       sync.RWMutex
	min      int64
	max      int64
	hmax     int64
	capacity int
	now      int64
	items    map[string]*idleEntry
	timers   timerHeap
	cleaned  int64
	timerOps int64
}

func NewIdleCleaner(min, max, hmax int64, capacity int) (*IdleCleaner, error) {
	if min < 1 || max < min || max > 1_000_000_000_000 ||
		hmax < min || hmax > 1_000_000_000_000 || capacity < 1 || capacity > 1_000_000 {
		return nil, ErrInvalidArgument
	}

	return &IdleCleaner{
		min:      min,
		max:      max,
		hmax:     hmax,
		capacity: capacity,
		items:    make(map[string]*idleEntry),
	}, nil
}

func (c *IdleCleaner) Touch(key string, now int64) ([]string, int64, error) {
	if key == "" || now < 0 || now > 1_000_000_000_000 {
		return nil, 0, ErrInvalidArgument
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.now {
		return nil, 0, ErrClockRolledBack
	}

	current := c.items[key]
	present := current != nil && current.timer > now
	if !present && c.timers.Len()-c.timers.DueCount(now)+1 > c.capacity {
		return nil, 0, ErrCapacityExceeded
	}

	cleaned := c.cleanDue(now)
	c.now = now

	entry := c.items[key]
	if entry == nil {
		entry = &idleEntry{
			key:     key,
			created: now,
			timer:   min64(now+c.max, now+c.hmax),
			index:   -1,
		}
		c.items[key] = entry
		c.timers.PushTimer(entry)
		c.timerOps++
	} else if now+c.min > entry.timer {
		nextTimer := min64(now+c.max, entry.created+c.hmax)
		if nextTimer != entry.timer {
			c.timers.RemoveAt(entry.index)
			entry.timer = nextTimer
			c.timers.PushTimer(entry)
			c.timerOps++
		}
	}

	return cleaned, c.items[key].timer, nil
}

func (c *IdleCleaner) Advance(now int64) ([]string, error) {
	if now < 0 || now > 1_000_000_000_000 {
		return nil, ErrInvalidArgument
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.now {
		return nil, ErrClockRolledBack
	}

	cleaned := c.cleanDue(now)
	c.now = now
	return cleaned, nil
}

func (c *IdleCleaner) Has(key string) bool {
	if key == "" {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.items[key] != nil
}

func (c *IdleCleaner) Timer(key string) (int64, error) {
	if key == "" {
		return 0, ErrInvalidArgument
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry := c.items[key]
	if entry == nil {
		return 0, ErrKeyNotFound
	}
	return entry.timer, nil
}

func (c *IdleCleaner) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

func (c *IdleCleaner) Cleaned() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cleaned
}

func (c *IdleCleaner) cleanDue(now int64) []string {
	cleaned := make([]string, 0)
	for c.timers.Len() > 0 && c.timers.PeekTimer().timer <= now {
		entry := c.timers.PopTimer()
		delete(c.items, entry.key)
		cleaned = append(cleaned, entry.key)
		c.cleaned++
	}
	return cleaned
}

func min64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}
