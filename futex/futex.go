// Package futex implements a futex-style wait queue subsystem with value
// validation, priority queues, bitset-filtered wakeups, requeueing,
// composite wake operations and injected-clock timeouts.
//
// All methods are safe for concurrent use; the result of concurrent calls
// is equivalent to some serial order. Replaying the same operation
// sequence yields identical return values, queues and states.
package futex

import (
	"container/heap"
	"container/list"
	"errors"
	"sync"
)

// Error values returned by Futex operations. They are distinguishable via
// errors.Is.
var (
	// ErrInvalidParam reports malformed arguments (negative tid/addr/n,
	// zero bitset, prio out of range, identical requeue addresses,
	// unknown op/cmp, negative clock).
	ErrInvalidParam = errors.New("futex: invalid parameter")
	// ErrBusy reports that the thread is already waiting.
	ErrBusy = errors.New("futex: thread already waiting")
	// ErrValueChanged reports that the memory word differs from expected.
	ErrValueChanged = errors.New("futex: value changed")
	// ErrImmediateTimeout reports a nonzero deadline not greater than now.
	ErrImmediateTimeout = errors.New("futex: deadline already passed")
	// ErrQueueFull reports that W waiters are already queued.
	ErrQueueFull = errors.New("futex: wait queue full")
	// ErrNotWaiting reports a Cancel for a thread that is not waiting.
	ErrNotWaiting = errors.New("futex: thread not waiting")
	// ErrTimeBackward reports an Advance to a moment before the clock.
	ErrTimeBackward = errors.New("futex: time goes backward")
)

// State is the lifecycle state of a thread.
type State int

const (
	// StateIdle is the initial state and the state before the first Wait.
	StateIdle State = iota
	// StateWaiting means the thread is queued on some address.
	StateWaiting
	// StateWoken means the thread was woken by Wake, Requeue or WakeOp.
	StateWoken
	// StateTimedOut means the thread's deadline passed under Advance.
	StateTimedOut
	// StateInterrupted means the thread was removed by Cancel.
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
	}
	return "unknown"
}

// Op is the memory-word mutation applied by WakeOp.
type Op int

const (
	// OpSet stores oparg.
	OpSet Op = iota
	// OpAdd stores old+oparg with int32 wraparound.
	OpAdd
	// OpOr stores old|oparg.
	OpOr
	// OpAndN stores old &^ oparg (and-not).
	OpAndN
	// OpXor stores old^oparg.
	OpXor
)

// Cmp is the comparison applied by WakeOp to the pre-write value.
type Cmp int

const (
	// CmpEQ is old == cmparg.
	CmpEQ Cmp = iota
	// CmpNE is old != cmparg.
	CmpNE
	// CmpLT is old < cmparg.
	CmpLT
	// CmpLE is old <= cmparg.
	CmpLE
	// CmpGT is old > cmparg.
	CmpGT
	// CmpGE is old >= cmparg.
	CmpGE
)

const (
	// MaxWaiters is the largest accepted constructor capacity.
	MaxWaiters = 1_000_000
	maxPrio    = 99
	prioLevels = maxPrio + 1
)

// waiter is one queued thread.
type waiter struct {
	tid      int64
	addr     int64
	bitset   uint32
	prio     int
	deadline int64 // absolute, 0 means never
	seq      uint64
	heapIdx  int // index in the timeout heap, -1 when absent
	elem     *list.Element
	queue    *addrQueue
}

// threadRec tracks one thread's state.
type threadRec struct {
	state State
	addr  int64 // valid while state == StateWaiting
	w     *waiter
}

// Futex is the wait queue subsystem. The zero value is not usable; call New.
type Futex struct {
	mu        sync.Mutex
	mem       map[int64]int32
	queues    map[int64]*addrQueue
	threads   map[int64]*threadRec
	deadlines timeoutHeap
	waiting   int
	cap       int
	seq       uint64
	now       int64
	examined  int64
}

// New builds a Futex allowing up to w simultaneous waiters. w must be in
// [1, MaxWaiters]; otherwise the whole configuration is rejected.
func New(w int) (*Futex, error) {
	if w < 1 || w > MaxWaiters {
		return nil, ErrInvalidParam
	}
	return &Futex{
		mem:     make(map[int64]int32),
		queues:  make(map[int64]*addrQueue),
		threads: make(map[int64]*threadRec),
		cap:     w,
	}, nil
}

// Store sets the memory word at addr. It never wakes anyone.
func (f *Futex) Store(addr int64, v int32) error {
	if addr < 0 {
		return ErrInvalidParam
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mem[addr] = v
	return nil
}

// Wait validates and enqueues tid on addr in one atomic step.
//
// Rejection order: invalid parameter, busy, value changed, immediate
// timeout, queue full. A rejected Wait changes nothing.
func (f *Futex) Wait(tid, addr int64, expected int32, bitset uint32, prio int, deadline int64) error {
	if tid < 0 || addr < 0 || deadline < 0 || bitset == 0 || prio < 0 || prio > maxPrio {
		return ErrInvalidParam
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.threads[tid]
	if rec != nil && rec.state == StateWaiting {
		return ErrBusy
	}
	if f.mem[addr] != expected {
		return ErrValueChanged
	}
	if deadline != 0 && deadline <= f.now {
		return ErrImmediateTimeout
	}
	if f.waiting >= f.cap {
		return ErrQueueFull
	}
	f.seq++
	w := &waiter{
		tid:      tid,
		addr:     addr,
		bitset:   bitset,
		prio:     prio,
		deadline: deadline,
		seq:      f.seq,
		heapIdx:  -1,
	}
	q := f.queues[addr]
	if q == nil {
		q = &addrQueue{}
		f.queues[addr] = q
	}
	q.pushBack(w)
	if rec == nil {
		rec = &threadRec{}
		f.threads[tid] = rec
	}
	rec.state = StateWaiting
	rec.addr = addr
	rec.w = w
	f.waiting++
	if deadline != 0 {
		f.track(w)
	}
	return nil
}

// Wake wakes up to n waiters on addr whose bitset intersects bitset,
// scanning from the head; mismatches stay in place.
func (f *Futex) Wake(addr int64, n int, bitset uint32) ([]int64, error) {
	if addr < 0 || n < 0 || bitset == 0 {
		return nil, ErrInvalidParam
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []int64{}
	if n == 0 {
		return out, nil
	}
	q := f.queues[addr]
	if q == nil {
		return out, nil
	}
	q.forEach(func(w *waiter) bool {
		f.examined++
		if w.bitset&bitset == 0 {
			return true // mismatch: skip, keep in place
		}
		out = append(out, w.tid)
		f.dequeueLocked(w, StateWoken)
		return len(out) < n
	})
	return out, nil
}

// Requeue wakes the first nWake waiters of a1 (ignoring bitsets) and moves
// up to nRequeue of the remaining head waiters to a2, preserving order.
// Moved waiters are appended to the tail of their prio group on a2 and
// keep their bitset, deadline and seq.
func (f *Futex) Requeue(a1, a2 int64, nWake, nRequeue int, check bool, expected int32) ([]int64, []int64, error) {
	if a1 < 0 || a2 < 0 || nWake < 0 || nRequeue < 0 || a1 == a2 {
		return nil, nil, ErrInvalidParam
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if check && f.mem[a1] != expected {
		return nil, nil, ErrValueChanged
	}
	woken := f.wakeHeadsLocked(a1, nWake)
	moved := []int64{}
	q1 := f.queues[a1]
	if q1 != nil {
		for i := 0; i < nRequeue; i++ {
			w := q1.popFront()
			if w == nil {
				break
			}
			f.examined++
			q2 := f.queues[a2]
			if q2 == nil {
				q2 = &addrQueue{}
				f.queues[a2] = q2
			}
			w.addr = a2
			q2.pushBack(w)
			f.threads[w.tid].addr = a2
			moved = append(moved, w.tid)
		}
	}
	return woken, moved, nil
}

// WakeOp atomically rewrites the word at a2 as op(old, oparg), wakes the
// first n1 waiters of a1, and, when cmp(old, cmparg) holds, wakes the
// first n2 waiters of a2.
func (f *Futex) WakeOp(a1, a2 int64, n1, n2 int, op Op, oparg int32, cmp Cmp, cmparg int32) ([]int64, []int64, error) {
	if a1 < 0 || a2 < 0 || n1 < 0 || n2 < 0 ||
		op < OpSet || op > OpXor || cmp < CmpEQ || cmp > CmpGE {
		return nil, nil, ErrInvalidParam
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	old := f.mem[a2]
	f.mem[a2] = applyOp(op, old, oparg)
	first := f.wakeHeadsLocked(a1, n1)
	second := []int64{}
	if applyCmp(cmp, old, cmparg) {
		second = f.wakeHeadsLocked(a2, n2)
	}
	return first, second, nil
}

// Cancel removes a waiting thread and marks it interrupted.
func (f *Futex) Cancel(tid int64) error {
	if tid < 0 {
		return ErrInvalidParam
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.threads[tid]
	if rec == nil || rec.state != StateWaiting {
		return ErrNotWaiting
	}
	f.dequeueLocked(rec.w, StateInterrupted)
	return nil
}

// Advance moves the clock to now and times out every waiter whose nonzero
// deadline is not greater than now, in (deadline, seq) order.
func (f *Futex) Advance(now int64) ([]int64, error) {
	if now < 0 {
		return nil, ErrInvalidParam
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if now < f.now {
		return nil, ErrTimeBackward
	}
	f.now = now
	out := []int64{}
	for len(f.deadlines) > 0 {
		f.examined++
		top := f.deadlines[0]
		if top.deadline > now {
			break
		}
		heap.Pop(&f.deadlines)
		out = append(out, top.tid)
		f.dequeueLocked(top, StateTimedOut)
	}
	return out, nil
}

// Waiters lists queued thread ids on addr in queue order.
func (f *Futex) Waiters(addr int64) []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.queues[addr]
	if q == nil {
		return nil
	}
	return q.tids()
}

// State reports the thread state and, while waiting, its address.
func (f *Futex) State(tid int64) (State, int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.threads[tid]
	if rec == nil {
		return StateIdle, 0
	}
	return rec.state, rec.addr
}

// Load reads the memory word at addr (0 when unset).
func (f *Futex) Load(addr int64) int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mem[addr]
}

// dequeueLocked removes w from its queue and timeout heap, updates the
// owning thread record and moves it to the given state.
func (f *Futex) dequeueLocked(w *waiter, state State) {
	if w.queue != nil {
		w.queue.remove(w)
	}
	f.untrack(w)
	rec := f.threads[w.tid]
	rec.state = state
	rec.addr = 0
	rec.w = nil
	f.waiting--
}

// wakeHeadsLocked wakes up to n head waiters of addr, ignoring bitsets.
func (f *Futex) wakeHeadsLocked(addr int64, n int) []int64 {
	out := []int64{}
	q := f.queues[addr]
	if q == nil {
		return out
	}
	for i := 0; i < n; i++ {
		w := q.popFront()
		if w == nil {
			break
		}
		f.examined++
		out = append(out, w.tid)
		f.dequeueLocked(w, StateWoken)
	}
	return out
}

func applyOp(op Op, old, arg int32) int32 {
	switch op {
	case OpSet:
		return arg
	case OpAdd:
		return old + arg // int32 wraparound
	case OpOr:
		return old | arg
	case OpAndN:
		return old &^ arg
	case OpXor:
		return old ^ arg
	}
	return old
}

func applyCmp(cmp Cmp, old, arg int32) bool {
	switch cmp {
	case CmpEQ:
		return old == arg
	case CmpNE:
		return old != arg
	case CmpLT:
		return old < arg
	case CmpLE:
		return old <= arg
	case CmpGT:
		return old > arg
	case CmpGE:
		return old >= arg
	}
	return false
}

// Examined reports the internal examined counter used to verify the
// complexity bounds of Wake, Requeue, WakeOp and Advance.
func (f *Futex) Examined() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.examined
}

// ResetExamined zeroes the examined counter.
func (f *Futex) ResetExamined() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.examined = 0
}
