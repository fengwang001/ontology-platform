package futex

import "container/heap"

// threadInfo tracks the current state of a thread. The state persists after
// wake/timeout/cancel until the thread's next successful Wait.
type threadInfo struct {
	state State
	addr  int64   // valid only while state == StateWaiting
	node  *waiter // valid only while state == StateWaiting
}

func (f *Futex) queue(addr int64) *addrQueue {
	q := f.queues[addr]
	if q == nil {
		q = newAddrQueue()
		f.queues[addr] = q
	}
	return q
}

// drop removes a waiting node from its address queue and the timeout heap,
// but leaves the thread state to the caller.
func (f *Futex) drop(w *waiter) {
	q := f.queues[w.addr]
	q.remove(w)
	if q.empty() {
		delete(f.queues, w.addr)
	}
	if w.heapIdx >= 0 {
		heap.Remove(&f.timeouts, w.heapIdx)
	}
	f.total--
}

func (f *Futex) waitingCount() int { return f.total }

// Store writes a memory word without waking anyone.
func (f *Futex) Store(addr int64, v int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mem[addr] = v
}

// Wait enqueues the thread if every precondition holds. Value checking and
// enqueueing form one atomic step.
func (f *Futex) Wait(tid int, addr int64, expected int32, bitset uint32, prio int, deadline int64) error {
	if tid < 0 || addr < 0 || deadline < 0 || bitset == 0 || prio < 0 || prio > 99 {
		return ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if ti := f.threads[tid]; ti != nil && ti.state == StateWaiting {
		return ErrBusy
	}
	if f.mem[addr] != expected {
		return ErrChanged
	}
	if deadline != 0 && deadline <= f.now {
		return ErrTimeout
	}
	if f.waitingCount() >= f.cap {
		return ErrFull
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
	f.queue(addr).append(w)
	if deadline != 0 {
		heap.Push(&f.timeouts, w)
	}
	f.total++
	f.threads[tid] = &threadInfo{state: StateWaiting, addr: addr, node: w}
	return nil
}

// Wake wakes up to n bitset-matching waiters at addr, scanning from the head.
// Non-matching waiters keep their position.
func (f *Futex) Wake(addr int64, n int, bitset uint32) ([]int, error) {
	if addr < 0 || n < 0 || bitset == 0 {
		return nil, ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	woken := []int{}
	if n == 0 {
		return woken, nil
	}
	q := f.queues[addr]
	if q == nil {
		return woken, nil
	}
	cur := q.head
	for cur != nil && len(woken) < n {
		f.examined["wake"]++
		next := cur.next
		if cur.bitset&bitset != 0 {
			tid := cur.tid
			f.drop(cur)
			f.threads[tid].state = StateWoken
			f.threads[tid].node = nil
			woken = append(woken, tid)
		}
		cur = next
	}
	return woken, nil
}

// wakeFirst removes and returns the tids of the first k waiters of addr,
// ignoring bitsets. It examines at most k nodes.
func (f *Futex) wakeFirst(kind string, addr int64, k int) []int {
	out := []int{}
	if k <= 0 {
		return out
	}
	q := f.queues[addr]
	if q == nil {
		return out
	}
	for k > 0 && q.head != nil {
		f.examined[kind]++
		w := q.head
		tid := w.tid
		f.drop(w)
		f.threads[tid].state = StateWoken
		f.threads[tid].node = nil
		out = append(out, tid)
		k--
	}
	return out
}

// Requeue wakes nWake waiters at a1 and moves up to nRequeue of the rest to a2.
func (f *Futex) Requeue(a1, a2 int64, nWake, nRequeue int, check bool, expected int32) (woken, requeued []int, err error) {
	if a1 < 0 || a2 < 0 || nWake < 0 || nRequeue < 0 || a1 == a2 {
		return nil, nil, ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if check && f.mem[a1] != expected {
		return nil, nil, ErrChanged
	}
	woken = f.wakeFirst("requeue", a1, nWake)
	requeued = []int{}
	if nRequeue == 0 {
		return woken, requeued, nil
	}
	q := f.queues[a1]
	if q == nil {
		return woken, requeued, nil
	}
	dst := f.queue(a2)
	moved := 0
	for q.head != nil && moved < nRequeue {
		f.examined["requeue"]++
		w := q.head
		tid := w.tid
		// Detach from the source queue only; deadline/bitset/seq are kept.
		q.remove(w)
		if q.empty() {
			delete(f.queues, a1)
		}
		w.addr = a2
		dst.append(w)
		f.threads[tid].addr = a2
		requeued = append(requeued, tid)
		moved++
	}
	return woken, requeued, nil
}

func applyOp(op Op, old, arg int32) (int32, bool) {
	switch op {
	case OpSet:
		return arg, true
	case OpAdd:
		return old + arg, true
	case OpOr:
		return old | arg, true
	case OpAndn:
		return old &^ arg, true
	case OpXor:
		return old ^ arg, true
	default:
		return 0, false
	}
}

func applyCmp(cmp Cmp, old, arg int32) bool {
	switch cmp {
	case CmpEq:
		return old == arg
	case CmpNe:
		return old != arg
	case CmpLt:
		return old < arg
	case CmpLe:
		return old <= arg
	case CmpGt:
		return old > arg
	case CmpGe:
		return old >= arg
	default:
		return false
	}
}

// WakeOp atomically mutates the word at a2, wakes the first n1 waiters at a1,
// and, when cmp holds against the pre-mutation value of a2, wakes the first
// n2 waiters at a2.
func (f *Futex) WakeOp(a1, a2 int64, n1, n2 int, op Op, oparg int32, cmp Cmp, cmparg int32) (w1, w2 []int, err error) {
	if a1 < 0 || a2 < 0 || n1 < 0 || n2 < 0 || !op.valid() || !cmp.valid() {
		return nil, nil, ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	old := f.mem[a2]
	nv, _ := applyOp(op, old, oparg)
	f.mem[a2] = nv
	w1 = f.wakeFirst("wakeop1", a1, n1)
	if applyCmp(cmp, old, cmparg) {
		w2 = f.wakeFirst("wakeop2", a2, n2)
	} else {
		w2 = []int{}
	}
	return w1, w2, nil
}

// Cancel removes a waiting thread and marks it interrupted.
func (f *Futex) Cancel(tid int) error {
	if tid < 0 {
		return ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ti := f.threads[tid]
	if ti == nil || ti.state != StateWaiting {
		return ErrNotWait
	}
	f.drop(ti.node)
	ti.state = StateInterrupted
	ti.node = nil
	return nil
}

// Advance pushes the clock forward and expires due waiters. Expired threads
// are returned in (deadline, seq) order using the timeout heap.
func (f *Futex) Advance(now int64) ([]int, error) {
	if now < 0 {
		return nil, ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if now < f.now {
		return nil, ErrClockBack
	}
	f.now = now
	expired := []int{}
	for f.timeouts.Len() > 0 {
		f.examined["advance"]++
		top := f.timeouts[0]
		if top.deadline == 0 || top.deadline > now {
			break
		}
		tid := top.tid
		f.drop(top)
		f.threads[tid].state = StateTimedOut
		f.threads[tid].node = nil
		expired = append(expired, tid)
	}
	return expired, nil
}

// Waiters returns the tids queued at addr in queue order.
func (f *Futex) Waiters(addr int64) []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.queues[addr]
	if q == nil {
		return []int{}
	}
	return q.tids()
}

// StateOf returns the thread state and, when waiting, its address.
func (f *Futex) StateOf(tid int) (state State, addr int64, err error) {
	if tid < 0 {
		return StateIdle, 0, ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ti := f.threads[tid]
	if ti == nil {
		return StateIdle, 0, nil
	}
	return ti.state, ti.addr, nil
}

// Load reads a memory word (default 0).
func (f *Futex) Load(addr int64) int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mem[addr]
}

// Now returns the injected clock value.
func (f *Futex) Now() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Examined returns the number of waiter nodes inspected per operation kind
// since the last ResetExamined.
func (f *Futex) Examined() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int, len(f.examined))
	for k, v := range f.examined {
		out[k] = v
	}
	return out
}

// ResetExamined zeroes the inspection counters.
func (f *Futex) ResetExamined() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.examined = make(map[string]int)
}
