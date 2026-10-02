package futex

// naiveModel is a straightforward reference implementation: one linear
// slice per address, with insertion by scanning for the first greater-prio
// node and appending at that prio group's tail.
type naiveWaiter struct {
	tid      int
	bitset   uint32
	prio     int
	deadline int64
	seq      int64
}

type naiveModel struct {
	cap     int
	now     int64
	seq     int64
	mem     map[int64]int32
	queues  map[int64][]*naiveWaiter
	states  map[int]State
	address map[int]int64
}

func newNaive(cap int) *naiveModel {
	return &naiveModel{
		cap:     cap,
		mem:     map[int64]int32{},
		queues:  map[int64][]*naiveWaiter{},
		states:  map[int]State{},
		address: map[int]int64{},
	}
}

func (m *naiveModel) state(tid int) State {
	if s, ok := m.states[tid]; ok {
		return s
	}
	return StateIdle
}

func (m *naiveModel) append(addr int64, w *naiveWaiter) {
	q := m.queues[addr]
	idx := len(q)
	for i, x := range q {
		if x.prio > w.prio {
			idx = i
			break
		}
	}
	q = append(q, nil)
	copy(q[idx+1:], q[idx:])
	q[idx] = w
	m.queues[addr] = q
}

func (m *naiveModel) remove(addr int64, idx int) *naiveWaiter {
	q := m.queues[addr]
	w := q[idx]
	m.queues[addr] = append(q[:idx], q[idx+1:]...)
	if len(m.queues[addr]) == 0 {
		delete(m.queues, addr)
	}
	return w
}

func (m *naiveModel) find(tid int) (int64, int) {
	for addr, q := range m.queues {
		for i, w := range q {
			if w.tid == tid {
				return addr, i
			}
		}
	}
	return -1, -1
}

func (m *naiveModel) wait(tid int, addr int64, expected int32, bitset uint32, prio int, deadline int64) error {
	if tid < 0 || addr < 0 || deadline < 0 || bitset == 0 || prio < 0 || prio > 99 {
		return ErrInvalid
	}
	if m.state(tid) == StateWaiting {
		return ErrBusy
	}
	if m.mem[addr] != expected {
		return ErrChanged
	}
	if deadline != 0 && deadline <= m.now {
		return ErrTimeout
	}
	total := 0
	for _, q := range m.queues {
		total += len(q)
	}
	if total >= m.cap {
		return ErrFull
	}
	m.seq++
	w := &naiveWaiter{tid: tid, bitset: bitset, prio: prio, deadline: deadline, seq: m.seq}
	m.append(addr, w)
	m.states[tid] = StateWaiting
	m.address[tid] = addr
	return nil
}

func (m *naiveModel) wake(addr int64, n int, bitset uint32) ([]int, error) {
	if addr < 0 || n < 0 || bitset == 0 {
		return nil, ErrInvalid
	}
	out := []int{}
	if n == 0 {
		return out, nil
	}
	q := m.queues[addr]
	i := 0
	for len(q) > 0 && i < len(q) && len(out) < n {
		w := q[i]
		if w.bitset&bitset != 0 {
			m.remove(addr, i)
			m.states[w.tid] = StateWoken
			out = append(out, w.tid)
			q = m.queues[addr] // elements shifted into i; do not advance
		} else {
			i++
			q = m.queues[addr]
		}
	}
	return out, nil
}

func (m *naiveModel) wakeFirst(addr int64, k int) []int {
	out := []int{}
	for k > 0 {
		q := m.queues[addr]
		if len(q) == 0 {
			break
		}
		w := m.remove(addr, 0)
		m.states[w.tid] = StateWoken
		out = append(out, w.tid)
		k--
	}
	return out
}

func (m *naiveModel) requeue(a1, a2 int64, nWake, nRequeue int, check bool, expected int32) ([]int, []int, error) {
	if a1 < 0 || a2 < 0 || nWake < 0 || nRequeue < 0 || a1 == a2 {
		return nil, nil, ErrInvalid
	}
	if check && m.mem[a1] != expected {
		return nil, nil, ErrChanged
	}
	wk := m.wakeFirst(a1, nWake)
	rq := []int{}
	for nRequeue > 0 {
		q := m.queues[a1]
		if len(q) == 0 {
			break
		}
		w := m.remove(a1, 0)
		m.append(a2, w)
		m.address[w.tid] = a2
		rq = append(rq, w.tid)
		nRequeue--
	}
	return wk, rq, nil
}

func (m *naiveModel) wakeOp(a1, a2 int64, n1, n2 int, op Op, oparg int32, cmp Cmp, cmparg int32) ([]int, []int, error) {
	if a1 < 0 || a2 < 0 || n1 < 0 || n2 < 0 || !op.valid() || !cmp.valid() {
		return nil, nil, ErrInvalid
	}
	old := m.mem[a2]
	nv, _ := applyOp(op, old, oparg)
	m.mem[a2] = nv
	w1 := m.wakeFirst(a1, n1)
	w2 := []int{}
	if applyCmp(cmp, old, cmparg) {
		w2 = m.wakeFirst(a2, n2)
	}
	return w1, w2, nil
}

func (m *naiveModel) cancel(tid int) error {
	if tid < 0 {
		return ErrInvalid
	}
	if m.state(tid) != StateWaiting {
		return ErrNotWait
	}
	addr, idx := m.find(tid)
	m.remove(addr, idx)
	m.states[tid] = StateInterrupted
	return nil
}

func (m *naiveModel) advance(now int64) ([]int, error) {
	if now < 0 {
		return nil, ErrInvalid
	}
	if now < m.now {
		return nil, ErrClockBack
	}
	m.now = now
	type item struct {
		deadline int64
		seq      int64
		tid      int
		addr     int64
		idx      int
	}
	var due []item
	for addr, q := range m.queues {
		for i, w := range q {
			if w.deadline != 0 && w.deadline <= now {
				due = append(due, item{w.deadline, w.seq, w.tid, addr, i})
			}
		}
	}
	// Sort by (deadline, seq) by repeated min extraction (keep it obviously
	// naive; traces are small).
	out := []int{}
	for len(due) > 0 {
		best := 0
		for i := 1; i < len(due); i++ {
			if due[i].deadline < due[best].deadline ||
				(due[i].deadline == due[best].deadline && due[i].seq < due[best].seq) {
				best = i
			}
		}
		it := due[best]
		due = append(due[:best], due[best+1:]...)
		// Locate again because earlier removals shift indices.
		addr, idx := m.find(it.tid)
		m.remove(addr, idx)
		m.states[it.tid] = StateTimedOut
		out = append(out, it.tid)
	}
	return out, nil
}

func (m *naiveModel) waiters(addr int64) []int {
	out := []int{}
	for _, w := range m.queues[addr] {
		out = append(out, w.tid)
	}
	return out
}
