package futex

import (
	"container/heap"
	"sync"
)

// State is the lifecycle state of a thread.
type State int

const (
	StateIdle State = iota
	StateWaiting
	StateWoken
	StateTimedOut
	StateInterrupted
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateWaiting:
		return "waiting"
	case StateWoken:
		return "woken"
	case StateTimedOut:
		return "timedout"
	case StateInterrupted:
		return "interrupted"
	default:
		return "unknown"
	}
}

// Op is the bitwise/arithmetic operation used by WakeOp.
type Op int

const (
	OpSet Op = iota
	OpAdd
	OpOr
	OpAndn
	OpXor
)

func (o Op) valid() bool { return o >= OpSet && o <= OpXor }

// Cmp is the comparison used by WakeOp against the old value.
type Cmp int

const (
	CmpEq Cmp = iota
	CmpNe
	CmpLt
	CmpLe
	CmpGt
	CmpGe
)

func (c Cmp) valid() bool { return c >= CmpEq && c <= CmpGe }

// Futex is a futex-like wait-queue subsystem driven by an injected clock.
type Futex struct {
	mu       sync.Mutex
	cap      int
	now      int64
	seq      int64
	mem      map[int64]int32
	queues   map[int64]*addrQueue
	threads  map[int]*threadInfo
	timeouts timeoutHeap
	total    int
	// examined counts waiter nodes inspected since the last ResetExamined,
	// broken down by operation kind.
	examined map[string]int
}

// New creates a Futex with capacity W (1..1_000_000).
func New(W int) *Futex {
	if W < 1 || W > 1_000_000 {
		panic("futex: W out of range [1, 1e6]")
	}
	f := &Futex{
		cap:      W,
		mem:      make(map[int64]int32),
		queues:   make(map[int64]*addrQueue),
		threads:  make(map[int]*threadInfo),
		timeouts: make(timeoutHeap, 0),
		examined: make(map[string]int),
	}
	heap.Init(&f.timeouts)
	return f
}
