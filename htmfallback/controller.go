// Package htmfallback implements a deterministic best-effort HTM fallback
// controller.
//
// All public operations are serialized internally so concurrent callers are
// equivalent to one valid serial ordering. Result counts reproduce exactly for
// the same sequence of calls.
package htmfallback

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidConfig  = errors.New("htmfallback: invalid configuration")
	ErrInvalidThread  = errors.New("htmfallback: invalid thread id")
	ErrInvalidState   = errors.New("htmfallback: invalid thread state")
	ErrInvalidAddress = errors.New("htmfallback: invalid address")
)

type ThreadState int

const (
	// StateIdle means the thread does not hold the lock or a transaction.
	StateIdle ThreadState = iota
	// StateSpeculative means the thread is executing a speculative transaction.
	StateSpeculative
	// StateFallback means the thread owns the global fallback lock.
	StateFallback
	// StateAborted means the latest speculative attempt was aborted.
	StateAborted
)

type AbortReason int

const (
	// ReasonNone is reported when no abort reason applies.
	ReasonNone AbortReason = iota
	// ReasonConflict is reported for a read/write-set conflict.
	ReasonConflict
	// ReasonCapacity is reported when a cache set exceeds its associativity.
	ReasonCapacity
	// ReasonLockHeld is reported when another thread enters the fallback lock.
	ReasonLockHeld
)

type ResultCounts struct {
	Retries              int
	Skip                 int
	ConsecutiveFallbacks int
}

type LockResult struct {
	Thread  int
	State   ThreadState
	Waited  bool
	Aborted []int
	Reason  AbortReason
	ResultCounts
}

type AccessResult struct {
	Thread  int
	Address int
	IsWrite bool
	State   ThreadState
	Aborted []int
	Reason  AbortReason
	ResultCounts
}

type UnlockResult struct {
	Thread int
	State  ThreadState
	ResultCounts
}

type ThreadSnapshot struct {
	State    ThreadState
	Reason   AbortReason
	Retries  int
	ReadSet  []int
	WriteSet []int
}

type Snapshot struct {
	Threads              []ThreadSnapshot
	Holder               *int
	Skip                 int
	ConsecutiveFallbacks int
}

type Controller struct {
	mu sync.Mutex

	n  int
	s  int
	w  int
	a  int
	r  int
	sk int
	f  int

	threads []threadState
	holder  int
	skip    int
	fb      int
}

type threadState struct {
	state   ThreadState
	reason  AbortReason
	retries int
	reads   map[int]struct{}
	writes  map[int]struct{}
}

// New validates and creates a controller with N threads, S cache sets, W ways,
// A addresses, retry limit R, skip length SK, and consecutive-fallback
// threshold F.
func New(threads, sets, ways, addresses, retries, skip, fallbacks int) (*Controller, error) {
	if threads < 1 || threads > 16 ||
		sets < 1 || sets > 8 ||
		ways < 1 || ways > 4 ||
		addresses < 1 || addresses > 64 ||
		retries < 0 || retries > 8 ||
		skip < 0 || skip > 8 ||
		fallbacks < 1 || fallbacks > 8 {
		return nil, ErrInvalidConfig
	}

	controller := &Controller{
		n:       threads,
		s:       sets,
		w:       ways,
		a:       addresses,
		r:       retries,
		sk:      skip,
		f:       fallbacks,
		threads: make([]threadState, threads),
		holder:  -1,
	}
	for index := range controller.threads {
		controller.threads[index].reads = make(map[int]struct{})
		controller.threads[index].writes = make(map[int]struct{})
	}

	return controller, nil
}

// Lock starts speculation or enters fallback according to the configured
// decision order. It returns Waited when the global lock is already held.
func (c *Controller) Lock(thread int) (LockResult, error) {
	if thread < 0 || thread >= c.n {
		return LockResult{}, ErrInvalidThread
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	current := &c.threads[thread]
	if current.state != StateIdle && current.state != StateAborted {
		return LockResult{}, ErrInvalidState
	}

	result := LockResult{
		Thread:       thread,
		State:        current.state,
		Reason:       current.reason,
		ResultCounts: c.resultCountsLocked(thread),
	}

	if c.holder >= 0 {
		result.Waited = true
		return result, nil
	}

	if current.state == StateAborted && current.reason == ReasonCapacity {
		c.enterFallbackLocked(thread, true, &result)
		return result, nil
	}

	if c.skip > 0 {
		c.skip--
		c.enterFallbackLocked(thread, false, &result)
		return result, nil
	}

	if current.retries > c.r {
		c.enterFallbackLocked(thread, true, &result)
		return result, nil
	}

	current.state = StateSpeculative
	current.reason = ReasonNone
	clearSet(current.reads)
	clearSet(current.writes)

	result.State = StateSpeculative
	result.Reason = ReasonNone
	result.ResultCounts = c.resultCountsLocked(thread)
	return result, nil
}

// Access records a speculative read or write. Conflict aborts are applied
// before the capacity check; fallback-mode accesses have no effect.
func (c *Controller) Access(thread, address int, isWrite bool) (AccessResult, error) {
	if thread < 0 || thread >= c.n {
		return AccessResult{}, ErrInvalidThread
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	current := &c.threads[thread]
	if current.state != StateSpeculative && current.state != StateFallback {
		return AccessResult{}, ErrInvalidState
	}
	if address < 0 || address >= c.a {
		return AccessResult{}, ErrInvalidAddress
	}

	result := AccessResult{
		Thread:       thread,
		Address:      address,
		IsWrite:      isWrite,
		State:        current.state,
		ResultCounts: c.resultCountsLocked(thread),
	}

	if current.state == StateFallback {
		return result, nil
	}

	for index := range c.threads {
		if index == thread {
			continue
		}

		other := &c.threads[index]
		if other.state != StateSpeculative {
			continue
		}

		conflicts := false
		if isWrite {
			if _, ok := other.reads[address]; ok {
				conflicts = true
			}
			if _, ok := other.writes[address]; ok {
				conflicts = true
			}
		} else if _, ok := other.writes[address]; ok {
			conflicts = true
		}

		if conflicts {
			other.state = StateAborted
			other.reason = ReasonConflict
			other.retries++
			clearSet(other.reads)
			clearSet(other.writes)
			result.Aborted = append(result.Aborted, index)
		}
	}

	if isWrite {
		current.writes[address] = struct{}{}
	} else {
		current.reads[address] = struct{}{}
	}

	group := address % c.s
	distinct := 0
	for candidate := range current.reads {
		if candidate%c.s == group {
			distinct++
		}
	}
	for candidate := range current.writes {
		if candidate%c.s == group {
			if _, read := current.reads[candidate]; !read {
				distinct++
			}
		}
	}

	if distinct > c.w {
		current.state = StateAborted
		current.reason = ReasonCapacity
		clearSet(current.reads)
		clearSet(current.writes)
		c.skip = c.sk

		result.State = StateAborted
		result.Reason = ReasonCapacity
		result.ResultCounts = c.resultCountsLocked(thread)
		return result, nil
	}

	result.State = StateSpeculative
	result.ResultCounts = c.resultCountsLocked(thread)
	return result, nil
}

// Unlock commits a speculative transaction or releases the fallback lock.
func (c *Controller) Unlock(thread int) (UnlockResult, error) {
	if thread < 0 || thread >= c.n {
		return UnlockResult{}, ErrInvalidThread
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	current := &c.threads[thread]
	if current.state != StateSpeculative && current.state != StateFallback {
		return UnlockResult{}, ErrInvalidState
	}

	wasSpeculative := current.state == StateSpeculative
	if !wasSpeculative {
		c.holder = -1
	}

	current.state = StateIdle
	current.reason = ReasonNone
	current.retries = 0
	clearSet(current.reads)
	clearSet(current.writes)

	if wasSpeculative {
		c.fb = 0
	}

	return UnlockResult{
		Thread:       thread,
		State:        StateIdle,
		ResultCounts: c.resultCountsLocked(thread),
	}, nil
}

// Snapshot returns a deterministic copy of thread states, sets, holder, and
// global counters.
func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	threads := make([]ThreadSnapshot, len(c.threads))
	for index := range c.threads {
		current := &c.threads[index]
		threads[index] = ThreadSnapshot{
			State:    current.state,
			Reason:   current.reason,
			Retries:  current.retries,
			ReadSet:  sortedKeys(current.reads),
			WriteSet: sortedKeys(current.writes),
		}
	}

	snapshot := Snapshot{
		Threads:              threads,
		Skip:                 c.skip,
		ConsecutiveFallbacks: c.fb,
	}
	if c.holder >= 0 {
		holder := c.holder
		snapshot.Holder = &holder
	}
	return snapshot
}

func (c *Controller) enterFallbackLocked(thread int, countsFallback bool, result *LockResult) {
	c.holder = thread
	current := &c.threads[thread]
	current.state = StateFallback
	current.reason = ReasonNone
	clearSet(current.reads)
	clearSet(current.writes)

	for index := range c.threads {
		other := &c.threads[index]
		if other.state == StateSpeculative {
			other.state = StateAborted
			other.reason = ReasonLockHeld
			clearSet(other.reads)
			clearSet(other.writes)
			result.Aborted = append(result.Aborted, index)
		}
	}

	if countsFallback {
		c.fb++
		if c.fb == c.f {
			c.skip = c.sk
			c.fb = 0
		}
	}

	result.State = StateFallback
	result.ResultCounts = c.resultCountsLocked(thread)
}

func (c *Controller) resultCountsLocked(thread int) ResultCounts {
	return ResultCounts{
		Retries:              c.threads[thread].retries,
		Skip:                 c.skip,
		ConsecutiveFallbacks: c.fb,
	}
}

func clearSet(set map[int]struct{}) {
	for address := range set {
		delete(set, address)
	}
}

func sortedKeys(set map[int]struct{}) []int {
	keys := make([]int, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}
