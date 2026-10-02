package ontology

import (
	"container/heap"
	"errors"
	"sync"
)

const (
	minTime int64 = 0
	maxTime int64 = 1_000_000_000_000
)

var (
	// ErrInvalidArgument is returned for invalid constructor or operation arguments.
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrClockRollback is returned when now is earlier than the current clock.
	ErrClockRollback = errors.New("clock rollback")
	// ErrCapacityLimit is returned before any state change when a new key exceeds K.
	ErrCapacityLimit = errors.New("capacity limit exceeded")
	// ErrNotFound is returned by Timer for a key absent from the state table.
	ErrNotFound = errors.New("key not found")
)

// IdleStateCleaner maintains expiring keys with one timer per key.
// Every Touch and Advance is atomic with respect to other callers.
type IdleStateCleaner struct {
	mu       sync.Mutex
	clock    int64
	min      int64
	max      int64
	maxLife  int64
	capacity int
	entries  map[string]*timerEntry
	timers   timerHeap
	cleaned  int64
	timerOps int64
}

type entry struct {
	created   int64
	timer     int64
	heapIndex int
	key       string
}

type timerEntry = entry

// NewIdleStateCleaner validates Min, Max, Hmax and K and returns a cleaner at T=0.
func NewIdleStateCleaner(minRetention, maxRetention, maxLifetime int64, capacity int) (*IdleStateCleaner, error) {
	if minRetention < 1 ||
		maxRetention < minRetention ||
		maxRetention > maxTime ||
		maxLifetime < minRetention ||
		maxLifetime > maxTime ||
		capacity < 1 || capacity > 1_000_000 {
		return nil, ErrInvalidArgument
	}

	cleaner := &IdleStateCleaner{
		min:      minRetention,
		max:      maxRetention,
		maxLife:  maxLifetime,
		capacity: capacity,
		entries:  make(map[string]*timerEntry),
	}
	heap.Init(&cleaner.timers)
	return cleaner, nil
}

// Touch advances the clock, removes due keys atomically, and then visits one key.
// It returns the due keys in cleanup order and the key's timer after the visit.
func (c *IdleStateCleaner) Touch(key []byte, now int64) ([]string, int64, error) {
	if len(key) == 0 || now < minTime || now > maxTime {
		return nil, 0, ErrInvalidArgument
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.clock {
		return nil, 0, ErrClockRollback
	}

	keyString := string(key)
	existing := c.entries[keyString]
	present := existing != nil && existing.timer > now
	if !present && len(c.entries)-c.timers.dueCount(now)+1 > c.capacity {
		return nil, 0, ErrCapacityLimit
	}

	cleaned := c.cleanDue(now)
	c.clock = now

	ent := c.entries[keyString]
	if ent == nil {
		ent = &timerEntry{
			key:     keyString,
			created: now,
			timer:   minInt64(now+c.max, now+c.maxLife),
		}
		c.entries[keyString] = ent
		heap.Push(&c.timers, ent)
		c.timerOps++
		return cleaned, ent.timer, nil
	}

	currentTimer := ent.timer
	if now+c.min > currentTimer {
		newTimer := minInt64(now+c.max, ent.created+c.maxLife)
		if newTimer != currentTimer {
			ent.timer = newTimer
			heap.Fix(&c.timers, ent.heapIndex)
			c.timerOps++
		}
	}

	return cleaned, ent.timer, nil
}

// Advance advances the clock and removes all due keys atomically.
func (c *IdleStateCleaner) Advance(now int64) ([]string, error) {
	if now < minTime || now > maxTime {
		return nil, ErrInvalidArgument
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.clock {
		return nil, ErrClockRollback
	}

	cleaned := c.cleanDue(now)
	c.clock = now
	return cleaned, nil
}

// Has reports whether a non-empty key is currently present.
func (c *IdleStateCleaner) Has(key []byte) bool {
	if len(key) == 0 {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	_, exists := c.entries[string(key)]
	return exists
}

// Timer returns the current timer for a non-empty key.
func (c *IdleStateCleaner) Timer(key []byte) (int64, error) {
	if len(key) == 0 {
		return 0, ErrInvalidArgument
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	ent, exists := c.entries[string(key)]
	if !exists {
		return 0, ErrNotFound
	}
	return ent.timer, nil
}

// Size returns the current number of keys.
func (c *IdleStateCleaner) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Cleaned returns the cumulative number of removed keys.
func (c *IdleStateCleaner) Cleaned() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cleaned
}

func (c *IdleStateCleaner) cleanDue(now int64) []string {
	cleaned := make([]string, 0)
	for len(c.timers) > 0 && c.timers[0].timer <= now {
		ent := heap.Pop(&c.timers).(*timerEntry)
		delete(c.entries, ent.key)
		cleaned = append(cleaned, ent.key)
		c.cleaned++
	}
	return cleaned
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

type timerHeap []*timerEntry

func (h timerHeap) Len() int {
	return len(h)
}

func (h timerHeap) Less(i, j int) bool {
	if h[i].timer != h[j].timer {
		return h[i].timer < h[j].timer
	}
	return h[i].key < h[j].key
}

func (h timerHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *timerHeap) Push(value any) {
	ent := value.(*timerEntry)
	ent.heapIndex = len(*h)
	*h = append(*h, ent)
}

func (h *timerHeap) Pop() any {
	old := *h
	last := len(old) - 1
	ent := old[last]
	ent.heapIndex = -1
	*h = old[:last]
	return ent
}

func (h timerHeap) dueCount(now int64) int {
	var count func(index int) int
	count = func(index int) int {
		if index >= len(h) || h[index].timer > now {
			return 0
		}
		return 1 + count(index*2+1) + count(index*2+2)
	}
	return count(0)
}
