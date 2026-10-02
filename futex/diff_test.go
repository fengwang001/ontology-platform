package futex

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
)

func errName(e error) string {
	switch e {
	case nil:
		return "ok"
	case ErrInvalid:
		return "invalid"
	case ErrBusy:
		return "busy"
	case ErrChanged:
		return "changed"
	case ErrTimeout:
		return "timeout"
	case ErrFull:
		return "full"
	case ErrNotWait:
		return "notwaiting"
	case ErrClockBack:
		return "clockback"
	default:
		return e.Error()
	}
}

type threadSnap struct {
	state State
	addr  int64
}

type snapshot struct {
	now     int64
	mem     map[int64]int32
	queues  map[int64][]int
	threads map[int]threadSnap
}

func snapshotReal(f *Futex, addrs []int64, tids []int) snapshot {
	s := snapshot{
		now:     f.Now(),
		mem:     map[int64]int32{},
		queues:  map[int64][]int{},
		threads: map[int]threadSnap{},
	}
	for _, a := range addrs {
		if v := f.Load(a); v != 0 {
			s.mem[a] = v
		}
		if q := f.Waiters(a); len(q) > 0 {
			s.queues[a] = append([]int(nil), q...)
		}
	}
	for _, tid := range tids {
		st, addr, _ := f.StateOf(tid)
		if st != StateIdle {
			s.threads[tid] = threadSnap{st, addr}
		}
	}
	return s
}

func snapshotModel(m *naiveModel, addrs []int64, tids []int) snapshot {
	s := snapshot{
		now:     m.now,
		mem:     map[int64]int32{},
		queues:  map[int64][]int{},
		threads: map[int]threadSnap{},
	}
	for _, a := range addrs {
		if v := m.mem[a]; v != 0 {
			s.mem[a] = v
		}
		if q := m.waiters(a); len(q) > 0 {
			s.queues[a] = append([]int(nil), q...)
		}
	}
	for _, tid := range tids {
		if st := m.state(tid); st != StateIdle {
			s.threads[tid] = threadSnap{st, m.address[tid]}
		}
	}
	return s
}

func sameSnapshot(a, b snapshot) (string, bool) {
	if a.now != b.now {
		return fmt.Sprintf("now %d != %d", a.now, b.now), false
	}
	if fmt.Sprint(a.mem) != fmt.Sprint(b.mem) {
		return fmt.Sprintf("mem %v != %v", a.mem, b.mem), false
	}
	if fmt.Sprint(a.queues) != fmt.Sprint(b.queues) {
		return fmt.Sprintf("queues %v != %v", a.queues, b.queues), false
	}
	if fmt.Sprint(a.threads) != fmt.Sprint(b.threads) {
		return fmt.Sprintf("threads %v != %v", a.threads, b.threads), false
	}
	return "", true
}

func mustState(f *Futex, tid int) State {
	st, _, _ := f.StateOf(tid)
	return st
}

func mustAddr(f *Futex, tid int) int64 {
	_, addr, _ := f.StateOf(tid)
	return addr
}

// TestRandomAgainstNaive replays 2000 random operation traces against both
// the real subsystem and the naive linear-slice model, comparing every
// return value and the full state after each step.
func TestRandomAgainstNaive(t *testing.T) {
	logAll := os.Getenv("FUTEX_TRACE_LOG") != ""
	const traces = 2000
	for seed := int64(0); seed < traces; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cap := 1 + rng.Intn(8)
		f := New(cap)
		m := newNaive(cap)
		var addrs []int64
		for a := int64(0); a < 4; a++ {
			addrs = append(addrs, a)
		}
		var tids []int
		for i := 0; i < 7; i++ {
			tids = append(tids, i)
		}

		var log strings.Builder
		fmt.Fprintf(&log, "--- seed=%d W=%d ---\n", seed, cap)
		fail := false
		var enqueued, wokenN, timedN, cancelN int
		steps := 30 + rng.Intn(60)
		for step := 0; step < steps; step++ {
			var input, output string
			kind := rng.Intn(11)
			switch kind {
			case 0:
				addr := addrs[rng.Intn(len(addrs))]
				v := int32(rng.Intn(11) - 5)
				input = fmt.Sprintf("Store(addr=%d,v=%d)", addr, v)
				f.Store(addr, v)
				m.mem[addr] = v
				output = "ok"
			case 1:
				tid := tids[rng.Intn(len(tids))]
				addr := addrs[rng.Intn(len(addrs))]
				expected := int32(rng.Intn(11) - 5)
				if rng.Intn(10) < 7 {
					expected = m.mem[addr]
				}
				bitset := []uint32{1, 2, 3, 7, 0x8, 0xFFFFFFFF}[rng.Intn(6)]
				prio := rng.Intn(101)
				var deadline int64
				switch rng.Intn(5) {
				case 0:
					deadline = 0
				case 1:
					deadline = m.now
				case 2:
					deadline = m.now + 1
				default:
					deadline = m.now + int64(rng.Intn(20))
				}
				input = fmt.Sprintf("Wait(tid=%d,addr=%d,exp=%d,bitset=%#x,prio=%d,dl=%d)",
					tid, addr, expected, bitset, prio, deadline)
				e1 := f.Wait(tid, addr, expected, bitset, prio, deadline)
				e2 := m.wait(tid, addr, expected, bitset, prio, deadline)
				if e1 == nil {
					enqueued++
				}
				output = fmt.Sprintf("real=%s model=%s", errName(e1), errName(e2))
				if errName(e1) != errName(e2) {
					fail = true
				}
			case 2:
				addr := addrs[rng.Intn(len(addrs))]
				n := rng.Intn(5) - 1
				bitset := []uint32{0, 1, 2, 3, 0xFFFFFFFF}[rng.Intn(5)]
				input = fmt.Sprintf("Wake(addr=%d,n=%d,bitset=%#x)", addr, n, bitset)
				w1, e1 := f.Wake(addr, n, bitset)
				w2, e2 := m.wake(addr, n, bitset)
				output = fmt.Sprintf("real=(%v,%s) model=(%v,%s)", w1, errName(e1), w2, errName(e2))
				if errName(e1) != errName(e2) || fmt.Sprint(w1) != fmt.Sprint(w2) {
					fail = true
				}
				wokenN += len(w1)
			case 3:
				a1 := addrs[rng.Intn(len(addrs))]
				a2 := addrs[rng.Intn(len(addrs))]
				nWake := rng.Intn(4) - 1
				nRq := rng.Intn(4)
				check := rng.Intn(2) == 0
				expected := m.mem[a1]
				if rng.Intn(2) == 0 {
					expected++
				}
				input = fmt.Sprintf("Requeue(a1=%d,a2=%d,nWake=%d,nRq=%d,check=%v,exp=%d)",
					a1, a2, nWake, nRq, check, expected)
				k1, r1, e1 := f.Requeue(a1, a2, nWake, nRq, check, expected)
				k2, r2, e2 := m.requeue(a1, a2, nWake, nRq, check, expected)
				output = fmt.Sprintf("real=(%v,%v,%s) model=(%v,%v,%s)",
					k1, r1, errName(e1), k2, r2, errName(e2))
				if errName(e1) != errName(e2) ||
					fmt.Sprint(k1) != fmt.Sprint(k2) || fmt.Sprint(r1) != fmt.Sprint(r2) {
					fail = true
				}
				wokenN += len(k1)
			case 4:
				a1 := addrs[rng.Intn(len(addrs))]
				a2 := addrs[rng.Intn(len(addrs))]
				n1 := rng.Intn(4)
				n2 := rng.Intn(4)
				op := []Op{OpSet, OpAdd, OpOr, OpAndn, OpXor, Op(99)}[rng.Intn(6)]
				cmp := []Cmp{CmpEq, CmpNe, CmpLt, CmpLe, CmpGt, CmpGe, Cmp(99)}[rng.Intn(7)]
				oparg := int32(rng.Intn(11) - 5)
				cmparg := int32(rng.Intn(11) - 5)
				input = fmt.Sprintf("WakeOp(a1=%d,a2=%d,n1=%d,n2=%d,op=%d,oparg=%d,cmp=%d,cmparg=%d)",
					a1, a2, n1, n2, op, oparg, cmp, cmparg)
				w1, w2, e1 := f.WakeOp(a1, a2, n1, n2, op, oparg, cmp, cmparg)
				v1, v2, e2 := m.wakeOp(a1, a2, n1, n2, op, oparg, cmp, cmparg)
				output = fmt.Sprintf("real=(%v,%v,%s) model=(%v,%v,%s)",
					w1, w2, errName(e1), v1, v2, errName(e2))
				if errName(e1) != errName(e2) ||
					fmt.Sprint(w1) != fmt.Sprint(v1) || fmt.Sprint(w2) != fmt.Sprint(v2) {
					fail = true
				}
				wokenN += len(w1) + len(w2)
			case 5:
				tid := -1
				if idx := rng.Intn(len(tids) + 1); idx < len(tids) {
					tid = tids[idx]
				}
				input = fmt.Sprintf("Cancel(tid=%d)", tid)
				e1 := f.Cancel(tid)
				e2 := m.cancel(tid)
				if e1 == nil {
					cancelN++
				}
				output = fmt.Sprintf("real=%s model=%s", errName(e1), errName(e2))
				if errName(e1) != errName(e2) {
					fail = true
				}
			case 6:
				var now int64
				if rng.Intn(5) == 0 {
					now = m.now - int64(1+rng.Intn(3))
				} else {
					now = m.now + int64(rng.Intn(8)) - 1
				}
				if rng.Intn(20) == 0 {
					now = -1
				}
				input = fmt.Sprintf("Advance(now=%d)", now)
				x1, e1 := f.Advance(now)
				x2, e2 := m.advance(now)
				output = fmt.Sprintf("real=(%v,%s) model=(%v,%s)", x1, errName(e1), x2, errName(e2))
				if errName(e1) != errName(e2) || fmt.Sprint(x1) != fmt.Sprint(x2) {
					fail = true
				}
				timedN += len(x1)
			default:
				addr := addrs[rng.Intn(len(addrs))]
				tid := tids[rng.Intn(len(tids))]
				input = fmt.Sprintf("Query(addr=%d,tid=%d)", addr, tid)
				output = fmt.Sprintf("real={load=%d,waiters=%v,state=%s@%d,now=%d} model={load=%d,waiters=%v,state=%s@%d,now=%d}",
					f.Load(addr), f.Waiters(addr),
					mustState(f, tid), mustAddr(f, tid), f.Now(),
					m.mem[addr], m.waiters(addr),
					m.state(tid), m.address[tid], m.now)
			}

			s1 := snapshotReal(f, addrs, tids)
			s2 := snapshotModel(m, addrs, tids)
			verdict := "state-match"
			if why, ok := sameSnapshot(s1, s2); !ok {
				verdict = "STATE-MISMATCH: " + why
				fail = true
			}
			fmt.Fprintf(&log, "step %d: %s => %s [%s]\n", step, input, output, verdict)
			if fail {
				break
			}
		}

		stillWaiting := 0
		seen := map[int]bool{}
		for _, a := range addrs {
			for _, tid := range f.Waiters(a) {
				if seen[tid] {
					t.Fatalf("tid %d present in two queues", tid)
				}
				seen[tid] = true
				stillWaiting++
			}
		}
		if enqueued != wokenN+timedN+cancelN+stillWaiting {
			fmt.Fprintf(&log, "BOOKKEEPING FAIL: enqueued=%d woken=%d timeout=%d cancel=%d waiting=%d\n",
				enqueued, wokenN, timedN, cancelN, stillWaiting)
			fail = true
		}
		if int64(enqueued) != f.seq {
			fmt.Fprintf(&log, "SEQ FAIL: seq=%d enqueued=%d\n", f.seq, enqueued)
			fail = true
		}

		if fail {
			t.Fatalf("trace mismatch\n%s", log.String())
		}
		if seed == 0 || logAll {
			t.Logf("\n%s", log.String())
		}
	}
	t.Logf("compared %d random traces against the naive model: all return values, queues and states matched", traces)
}
