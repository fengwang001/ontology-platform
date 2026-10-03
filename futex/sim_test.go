package futex

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// naive is a straightforward reference implementation of the specification:
// one linear slice per address, linear scans everywhere. It exists only to
// be cross-checked against Futex on random operation sequences.
type naive struct {
	mem     map[int64]int32
	qs      map[int64][]*nWaiter
	states  map[int64]State
	waddr   map[int64]int64
	waiting int
	cap     int
	seq     uint64
	now     int64
}

type nWaiter struct {
	tid      int64
	bitset   uint32
	prio     int
	deadline int64
	seq      uint64
}

func newNaive(capacity int) *naive {
	return &naive{
		mem:    make(map[int64]int32),
		qs:     make(map[int64][]*nWaiter),
		states: make(map[int64]State),
		waddr:  make(map[int64]int64),
		cap:    capacity,
	}
}

func (n *naive) Store(addr int64, v int32) error {
	if addr < 0 {
		return ErrInvalidParam
	}
	n.mem[addr] = v
	return nil
}

func (n *naive) Wait(tid, addr int64, expected int32, bitset uint32, prio int, deadline int64) error {
	if tid < 0 || addr < 0 || deadline < 0 || bitset == 0 || prio < 0 || prio > maxPrio {
		return ErrInvalidParam
	}
	if n.states[tid] == StateWaiting {
		return ErrBusy
	}
	if n.mem[addr] != expected {
		return ErrValueChanged
	}
	if deadline != 0 && deadline <= n.now {
		return ErrImmediateTimeout
	}
	if n.waiting >= n.cap {
		return ErrQueueFull
	}
	n.seq++
	w := &nWaiter{tid: tid, bitset: bitset, prio: prio, deadline: deadline, seq: n.seq}
	q := n.qs[addr]
	pos := len(q)
	for i, e := range q {
		if e.prio > prio {
			pos = i
			break
		}
	}
	q = append(q, nil)
	copy(q[pos+1:], q[pos:])
	q[pos] = w
	n.qs[addr] = q
	n.states[tid] = StateWaiting
	n.waddr[tid] = addr
	n.waiting++
	return nil
}

// removeAt detaches index i of the queue on addr and marks the thread.
func (n *naive) removeAt(addr int64, i int, state State) int64 {
	q := n.qs[addr]
	tid := q[i].tid
	n.qs[addr] = append(q[:i], q[i+1:]...)
	n.states[tid] = state
	delete(n.waddr, tid)
	n.waiting--
	return tid
}

func (n *naive) Wake(addr int64, count int, bitset uint32) ([]int64, error) {
	if addr < 0 || count < 0 || bitset == 0 {
		return nil, ErrInvalidParam
	}
	out := []int64{}
	q := n.qs[addr]
	for i := 0; i < len(q) && len(out) < count; {
		if q[i].bitset&bitset != 0 {
			out = append(out, n.removeAt(addr, i, StateWoken))
			q = n.qs[addr]
			continue
		}
		i++
	}
	return out, nil
}

func (n *naive) wakeHeads(addr int64, count int) []int64 {
	out := []int64{}
	for len(out) < count && len(n.qs[addr]) > 0 {
		out = append(out, n.removeAt(addr, 0, StateWoken))
	}
	return out
}

func (n *naive) Requeue(a1, a2 int64, nWake, nRequeue int, check bool, expected int32) ([]int64, []int64, error) {
	if a1 < 0 || a2 < 0 || nWake < 0 || nRequeue < 0 || a1 == a2 {
		return nil, nil, ErrInvalidParam
	}
	if check && n.mem[a1] != expected {
		return nil, nil, ErrValueChanged
	}
	woken := n.wakeHeads(a1, nWake)
	moved := []int64{}
	for len(moved) < nRequeue && len(n.qs[a1]) > 0 {
		w := n.qs[a1][0]
		n.qs[a1] = n.qs[a1][1:]
		q := n.qs[a2]
		pos := len(q)
		for i, e := range q {
			if e.prio > w.prio {
				pos = i
				break
			}
		}
		q = append(q, nil)
		copy(q[pos+1:], q[pos:])
		q[pos] = w
		n.qs[a2] = q
		n.waddr[w.tid] = a2
		moved = append(moved, w.tid)
	}
	return woken, moved, nil
}

func (n *naive) WakeOp(a1, a2 int64, n1, n2 int, op Op, oparg int32, cmp Cmp, cmparg int32) ([]int64, []int64, error) {
	if a1 < 0 || a2 < 0 || n1 < 0 || n2 < 0 ||
		op < OpSet || op > OpXor || cmp < CmpEQ || cmp > CmpGE {
		return nil, nil, ErrInvalidParam
	}
	old := n.mem[a2]
	n.mem[a2] = applyOp(op, old, oparg)
	first := n.wakeHeads(a1, n1)
	second := []int64{}
	if applyCmp(cmp, old, cmparg) {
		second = n.wakeHeads(a2, n2)
	}
	return first, second, nil
}

func (n *naive) Cancel(tid int64) error {
	if tid < 0 {
		return ErrInvalidParam
	}
	if n.states[tid] != StateWaiting {
		return ErrNotWaiting
	}
	addr := n.waddr[tid]
	for i, w := range n.qs[addr] {
		if w.tid == tid {
			n.removeAt(addr, i, StateInterrupted)
			return nil
		}
	}
	panic("naive: waiting thread not found in queue")
}

func (n *naive) Advance(now int64) ([]int64, error) {
	if now < 0 {
		return nil, ErrInvalidParam
	}
	if now < n.now {
		return nil, ErrTimeBackward
	}
	n.now = now
	type due struct {
		a int64
		w *nWaiter
	}
	var dues []due
	for a, q := range n.qs {
		for _, w := range q {
			if w.deadline != 0 && w.deadline <= now {
				dues = append(dues, due{a: a, w: w})
			}
		}
	}
	sort.Slice(dues, func(i, j int) bool {
		if dues[i].w.deadline != dues[j].w.deadline {
			return dues[i].w.deadline < dues[j].w.deadline
		}
		return dues[i].w.seq < dues[j].w.seq
	})
	out := []int64{}
	for _, d := range dues {
		for i, w := range n.qs[d.a] {
			if w == d.w {
				out = append(out, n.removeAt(d.a, i, StateTimedOut))
				break
			}
		}
	}
	return out, nil
}

func (n *naive) Waiters(addr int64) []int64 {
	var out []int64
	for _, w := range n.qs[addr] {
		out = append(out, w.tid)
	}
	return out
}

func (n *naive) State(tid int64) (State, int64) {
	return n.states[tid], n.waddr[tid]
}

func (n *naive) Load(addr int64) int32 {
	return n.mem[addr]
}

// TestRandomAgainstNaive replays 2000 random operation sequences against
// both Futex and the naive reference, comparing every return value, queue,
// thread state and memory word. Inputs, outputs and the verdict basis are
// logged (visible with go test -v).
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	const opsPerSeq = 150
	addrs := []int64{0, 1, 2, 3, 100, 200, 300}
	tids := []int64{}
	for i := int64(0); i < 25; i++ {
		tids = append(tids, i)
	}
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		capacity := 1 + rng.Intn(40)
		f := mustNew(t, capacity)
		n := newNaive(capacity)
		var trace []string
		logDetail := seed < 3 // log a few sequences in full
		for step := 0; step < opsPerSeq; step++ {
			desc, verdict := runRandomOp(t, rng, f, n, addrs, tids)
			trace = append(trace, desc)
			if logDetail {
				t.Logf("seed=%d step=%d op=%s -> %s", seed, step, desc, verdict)
			}
			if verdict != "ok" {
				for i, d := range trace {
					t.Logf("trace[%d]: %s", i, d)
				}
				t.Fatalf("seed=%d step=%d op=%s mismatch: %s", seed, step, desc, verdict)
			}
			if step%10 == 9 {
				if basis := compareFullState(f, n, addrs, tids); basis != "" {
					for i, d := range trace {
						t.Logf("trace[%d]: %s", i, d)
					}
					t.Fatalf("seed=%d step=%d state divergence: %s", seed, step, basis)
				}
			}
		}
		if basis := compareFullState(f, n, addrs, tids); basis != "" {
			t.Fatalf("seed=%d final state divergence: %s", seed, basis)
		}
		f.checkInvariants(t)
		t.Logf("seed=%d cap=%d ops=%d verdict=match (returns, queues, states, memory identical)", seed, capacity, opsPerSeq)
	}
}

// runRandomOp executes one random operation on both implementations and
// returns a description plus a verdict ("ok" or a mismatch explanation).
func runRandomOp(t *testing.T, rng *rand.Rand, f *Futex, n *naive, addrs, tids []int64) (string, string) {
	addr := func() int64 { return addrs[rng.Intn(len(addrs))] }
	tid := func() int64 { return tids[rng.Intn(len(tids))] }
	maybeNegAddr := func() int64 {
		if rng.Intn(20) == 0 {
			return -1
		}
		return addr()
	}
	bitset := func() uint32 {
		if rng.Intn(20) == 0 {
			return 0 // invalid on purpose
		}
		return []uint32{0x1, 0x2, 0x4, 0x8, 0x3, 0x5, 0xFFFFFFFF}[rng.Intn(7)]
	}
	prio := func() int {
		if rng.Intn(20) == 0 {
			return []int{-1, 100}[rng.Intn(2)] // invalid on purpose
		}
		return rng.Intn(6)
	}
	small := func() int32 { return int32(rng.Intn(5)) - 1 }
	num := func() int {
		if rng.Intn(20) == 0 {
			return -1 // invalid on purpose
		}
		return rng.Intn(6)
	}
	deadline := func(now int64) int64 {
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4, 5:
			return 0
		case 6:
			return -1 // invalid on purpose
		default:
			return now + int64(rng.Intn(25)) - 2
		}
	}

	switch rng.Intn(100) {
	case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
		20, 21, 22, 23, 24, 25, 26, 27, 28, 29: // Wait 30%
		ti, a, e, b, p := tid(), maybeNegAddr(), small(), bitset(), prio()
		d := deadline(n.now)
		errF := f.Wait(ti, a, e, b, p, d)
		errN := n.Wait(ti, a, e, b, p, d)
		desc := fmt.Sprintf("Wait(tid=%d addr=%d exp=%d bits=%#x prio=%d dl=%d)", ti, a, e, b, p, d)
		return desc, cmpErr(errF, errN)
	case 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44: // Wake 15%
		a, c, b := maybeNegAddr(), num(), bitset()
		idsF, errF := f.Wake(a, c, b)
		idsN, errN := n.Wake(a, c, b)
		desc := fmt.Sprintf("Wake(addr=%d n=%d bits=%#x)", a, c, b)
		if v := cmpErr(errF, errN); v != "ok" {
			return desc, v
		}
		return desc, cmpIDs(idsF, idsN, "wake")
	case 45, 46, 47, 48, 49, 50, 51, 52, 53, 54: // Store 10%
		a, v := maybeNegAddr(), small()
		errF := f.Store(a, v)
		errN := n.Store(a, v)
		desc := fmt.Sprintf("Store(addr=%d v=%d)", a, v)
		return desc, cmpErr(errF, errN)
	case 55, 56, 57, 58, 59, 60, 61, 62, 63, 64: // Requeue 10%
		a1, a2 := maybeNegAddr(), maybeNegAddr()
		nw, nr := num(), num()
		check := rng.Intn(2) == 0
		exp := small()
		wF, mF, errF := f.Requeue(a1, a2, nw, nr, check, exp)
		wN, mN, errN := n.Requeue(a1, a2, nw, nr, check, exp)
		desc := fmt.Sprintf("Requeue(a1=%d a2=%d nWake=%d nRequeue=%d check=%v exp=%d)", a1, a2, nw, nr, check, exp)
		if v := cmpErr(errF, errN); v != "ok" {
			return desc, v
		}
		if v := cmpIDs(wF, wN, "requeue-woken"); v != "ok" {
			return desc, v
		}
		return desc, cmpIDs(mF, mN, "requeue-moved")
	case 65, 66, 67, 68, 69, 70, 71, 72, 73, 74: // WakeOp 10%
		a1, a2 := maybeNegAddr(), maybeNegAddr()
		n1, n2 := num(), num()
		op := Op(rng.Intn(5))
		cmp := Cmp(rng.Intn(6))
		if rng.Intn(20) == 0 {
			op = Op(99)
		}
		if rng.Intn(20) == 0 {
			cmp = Cmp(99)
		}
		oa, ca := small(), small()
		fF, sF, errF := f.WakeOp(a1, a2, n1, n2, op, oa, cmp, ca)
		fN, sN, errN := n.WakeOp(a1, a2, n1, n2, op, oa, cmp, ca)
		desc := fmt.Sprintf("WakeOp(a1=%d a2=%d n1=%d n2=%d op=%d oparg=%d cmp=%d cmparg=%d)", a1, a2, n1, n2, op, oa, cmp, ca)
		if v := cmpErr(errF, errN); v != "ok" {
			return desc, v
		}
		if v := cmpIDs(fF, fN, "wakeop-first"); v != "ok" {
			return desc, v
		}
		return desc, cmpIDs(sF, sN, "wakeop-second")
	case 75, 76, 77, 78, 79, 80, 81, 82, 83, 84: // Cancel 10%
		ti := tid()
		if rng.Intn(20) == 0 {
			ti = -1
		}
		errF := f.Cancel(ti)
		errN := n.Cancel(ti)
		desc := fmt.Sprintf("Cancel(tid=%d)", ti)
		return desc, cmpErr(errF, errN)
	default: // Advance 15%
		var now int64
		switch rng.Intn(10) {
		case 0:
			now = -1
		case 1:
			now = n.now - int64(rng.Intn(5)+1)
		default:
			now = n.now + int64(rng.Intn(12))
		}
		idsF, errF := f.Advance(now)
		idsN, errN := n.Advance(now)
		desc := fmt.Sprintf("Advance(now=%d)", now)
		if v := cmpErr(errF, errN); v != "ok" {
			return desc, v
		}
		return desc, cmpIDs(idsF, idsN, "advance")
	}
}

func cmpErr(errF, errN error) string {
	if errF != errN {
		return fmt.Sprintf("error mismatch: futex=%v naive=%v", errF, errN)
	}
	return "ok"
}

func cmpIDs(a, b []int64, what string) string {
	na := append([]int64{}, a...)
	nb := append([]int64{}, b...)
	if !reflect.DeepEqual(na, nb) {
		return fmt.Sprintf("%s mismatch: futex=%v naive=%v", what, na, nb)
	}
	return "ok"
}

// compareFullState cross-checks queues, states and memory words.
func compareFullState(f *Futex, n *naive, addrs, tids []int64) string {
	for _, a := range addrs {
		if v := cmpIDs(f.Waiters(a), n.Waiters(a), fmt.Sprintf("Waiters(%d)", a)); v != "ok" {
			return v
		}
		if f.Load(a) != n.Load(a) {
			return fmt.Sprintf("Load(%d): futex=%d naive=%d", a, f.Load(a), n.Load(a))
		}
	}
	for _, ti := range tids {
		sF, aF := f.State(ti)
		sN, aN := n.State(ti)
		if sF != sN || (sF == StateWaiting && aF != aN) {
			return fmt.Sprintf("State(%d): futex=(%v,%d) naive=(%v,%d)", ti, sF, aF, sN, aN)
		}
	}
	return ""
}
