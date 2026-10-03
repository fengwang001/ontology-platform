// Package htm implements a best-effort hardware transactional memory
// (HTM) lock-elision fallback controller.
//
// Critical sections of N threads first execute speculatively. Abort
// reasons are classified as conflict (read/write-set intersection),
// capacity (per-set way overflow) or lock-held (a fallback thread took
// the global lock). A retry budget and a skip counter decide when
// threads fall back to the global lock. All calls are serialized by a
// single mutex, so concurrent invocation is equivalent to some serial
// order and replays deterministically.
package htm

import (
	"errors"
	"sync"
)

// Config holds the constructor parameters of a Controller.
type Config struct {
	N  int // thread count, 1..16, ids 0..N-1
	S  int // cache set count, 1..8, address a maps to set a%S
	W  int // ways per set, 1..4
	A  int // cache-line address count, 1..64, addresses 0..A-1
	R  int // retry limit for conflict aborts, 0..8
	SK int // skip length after capacity abort / fallback streak, 0..8
	F  int // consecutive-fallback threshold, 1..8
}

// Validation errors. Rejected calls never mutate any state.
var (
	ErrInvalidConfig  = errors.New("htm: invalid configuration")
	ErrInvalidThread  = errors.New("htm: thread id out of range")
	ErrInvalidState   = errors.New("htm: thread state does not allow this call")
	ErrInvalidAddress = errors.New("htm: address out of range")
)

// State is the per-thread state.
type State int

const (
	Idle        State = iota // not in any critical section
	Speculating              // executing transactionally
	Fallback                 // holding the global lock
	Aborted                  // transaction aborted, not yet re-locked
)

// AbortReason classifies why a speculative thread aborted.
type AbortReason int

const (
	NoAbort  AbortReason = iota
	Conflict             // read/write-set intersection with another speculative thread
	Capacity             // per-set distinct address overflow
	LockHeld             // another thread took the global lock
)

// LockRule records which Lock decision rule fired (the decision basis).
type LockRule int

const (
	RuleWait          LockRule = iota // 1: lock holder exists, wait
	RuleCapacityAbort                 // 2: previously capacity-aborted, fall back
	RuleSkip                          // 3: skip counter consumed, fall back
	RuleRetryBudget                   // 4: conflict retries exhausted, fall back
	RuleSpeculate                     // 5: enter speculative execution
)

// LockResult is the outcome of Lock.
type LockResult struct {
	Rule    LockRule
	Aborted []int // threads aborted with LockHeld, ascending; nil unless fallback
}

// AccessOutcome records the Access decision basis.
type AccessOutcome int

const (
	AccessFallbackNoOp  AccessOutcome = iota // fallback thread: no effect
	AccessOK                                 // recorded, no capacity overflow
	AccessCapacityAbort                      // self capacity abort
)

// AccessResult is the outcome of Access.
type AccessResult struct {
	Outcome AccessOutcome
	Aborted []int // threads aborted with Conflict, ascending
}

// ThreadSnapshot is an exported view of one thread's state.
type ThreadSnapshot struct {
	State  State
	Reason AbortReason
	R      int   // conflict abort count
	Read   []int // speculative read set, ascending
	Write  []int // speculative write set, ascending
}

// Snapshot is an exported view of the whole controller.
type Snapshot struct {
	Threads []ThreadSnapshot
	Holder  int // lock holder, -1 if none
	Skip    int
	Fb      int
}

type thread struct {
	state  State
	reason AbortReason
	r      int
	read   map[int]bool
	write  map[int]bool
}

// Controller is a best-effort HTM lock-elision fallback controller.
// All methods are safe for concurrent use.
type Controller struct {
	cfg     Config
	mu      sync.Mutex
	threads []thread
	holder  int
	skip    int
	fb      int
}

// NewController validates cfg and returns a ready Controller.
// Any out-of-range parameter rejects the whole configuration.
func NewController(cfg Config) (*Controller, error) {
	if cfg.N < 1 || cfg.N > 16 ||
		cfg.S < 1 || cfg.S > 8 ||
		cfg.W < 1 || cfg.W > 4 ||
		cfg.A < 1 || cfg.A > 64 ||
		cfg.R < 0 || cfg.R > 8 ||
		cfg.SK < 0 || cfg.SK > 8 ||
		cfg.F < 1 || cfg.F > 8 {
		return nil, ErrInvalidConfig
	}
	return &Controller{cfg: cfg, threads: make([]thread, cfg.N), holder: -1}, nil
}

// Lock implements the five-rule lock decision. t must be Idle or Aborted.
func (c *Controller) Lock(t int) (LockResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t < 0 || t >= c.cfg.N {
		return LockResult{}, ErrInvalidThread
	}
	th := &c.threads[t]
	if th.state != Idle && th.state != Aborted {
		return LockResult{}, ErrInvalidState
	}
	// Rule 1: a holder exists, wait without changing any state.
	if c.holder != -1 {
		return LockResult{Rule: RuleWait}, nil
	}
	fallback := false
	countFb := false
	rule := RuleSpeculate
	switch {
	// Rule 2: previously capacity-aborted, fall back directly.
	case th.state == Aborted && th.reason == Capacity:
		fallback, countFb, rule = true, true, RuleCapacityAbort
	// Rule 3: consume one skip slot and fall back (does not count fb).
	case c.skip > 0:
		c.skip--
		fallback, rule = true, RuleSkip
	// Rule 4: conflict retry budget exhausted, fall back.
	case th.r > c.cfg.R:
		fallback, countFb, rule = true, true, RuleRetryBudget
	}
	if !fallback {
		// Rule 5: speculate with cleared read/write sets.
		th.state = Speculating
		th.reason = NoAbort
		th.read = map[int]bool{}
		th.write = map[int]bool{}
		return LockResult{Rule: RuleSpeculate}, nil
	}
	// Fallback path: take the global lock, abort every other
	// speculative thread with LockHeld (does not bump their r).
	aborted := []int{}
	for i := range c.threads {
		if i == t {
			continue
		}
		o := &c.threads[i]
		if o.state == Speculating {
			o.state = Aborted
			o.reason = LockHeld
			o.read, o.write = nil, nil
			aborted = append(aborted, i)
		}
	}
	c.holder = t
	th.state = Fallback
	th.reason = NoAbort
	th.read, th.write = nil, nil
	if countFb {
		c.fb++
		if c.fb == c.cfg.F {
			c.skip = c.cfg.SK
			c.fb = 0
		}
	}
	return LockResult{Rule: rule, Aborted: aborted}, nil
}

// Access records a speculative access to address a. t must be
// Speculating or Fallback; a fallback thread's access is a no-op.
func (c *Controller) Access(t, a int, write bool) (AccessResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t < 0 || t >= c.cfg.N {
		return AccessResult{}, ErrInvalidThread
	}
	th := &c.threads[t]
	if th.state != Speculating && th.state != Fallback {
		return AccessResult{}, ErrInvalidState
	}
	if a < 0 || a >= c.cfg.A {
		return AccessResult{}, ErrInvalidAddress
	}
	if th.state == Fallback {
		return AccessResult{Outcome: AccessFallbackNoOp}, nil
	}
	// Step 1: abort every other speculative thread whose sets
	// intersect this access (write vs read|write, read vs write).
	aborted := []int{}
	for i := range c.threads {
		if i == t {
			continue
		}
		o := &c.threads[i]
		if o.state != Speculating {
			continue
		}
		conflict := o.write[a] || (write && o.read[a])
		if conflict {
			o.state = Aborted
			o.reason = Conflict
			o.r++
			o.read, o.write = nil, nil
			aborted = append(aborted, i)
		}
	}
	// Step 2: record the address in t's read or write set.
	if write {
		th.write[a] = true
	} else {
		th.read[a] = true
	}
	// Step 3: capacity check on the set of address a. Only this set's
	// distinct count can grow on this access, so checking it suffices.
	set := a % c.cfg.S
	distinct := map[int]bool{}
	for addr := range th.read {
		if addr%c.cfg.S == set {
			distinct[addr] = true
		}
	}
	for addr := range th.write {
		if addr%c.cfg.S == set {
			distinct[addr] = true
		}
	}
	if len(distinct) > c.cfg.W {
		th.state = Aborted
		th.reason = Capacity
		th.read, th.write = nil, nil
		c.skip = c.cfg.SK
		return AccessResult{Outcome: AccessCapacityAbort, Aborted: aborted}, nil
	}
	return AccessResult{Outcome: AccessOK, Aborted: aborted}, nil
}

// Unlock commits a speculative thread or releases the global lock.
func (c *Controller) Unlock(t int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t < 0 || t >= c.cfg.N {
		return ErrInvalidThread
	}
	th := &c.threads[t]
	switch th.state {
	case Speculating:
		// Commit: clear sets, reset the fallback streak.
		th.read, th.write = nil, nil
		c.fb = 0
	case Fallback:
		c.holder = -1
	default:
		return ErrInvalidState
	}
	th.state = Idle
	th.reason = NoAbort
	th.r = 0
	return nil
}

// Snapshot returns a consistent copy of the controller state.
func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap := Snapshot{
		Threads: make([]ThreadSnapshot, len(c.threads)),
		Holder:  c.holder,
		Skip:    c.skip,
		Fb:      c.fb,
	}
	for i := range c.threads {
		th := &c.threads[i]
		snap.Threads[i] = ThreadSnapshot{
			State:  th.state,
			Reason: th.reason,
			R:      th.r,
			Read:   sortedKeys(th.read),
			Write:  sortedKeys(th.write),
		}
	}
	return snap
}

func sortedKeys(m map[int]bool) []int {
	keys := []int{}
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
