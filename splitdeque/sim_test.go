package splitdeque

import (
	"fmt"
	"math/rand"
	"testing"
)

// naiveDeque is a line-by-line reimplementation of the specification,
// independent of the production code: elements live in a growing slice
// indexed by absolute logical indices, and release/reclaim move nothing.
type naiveDeque struct {
	cap, sm, rv, f int64
	buf            []int64
	t, s, b        int64
	fl             bool
	ag, dm         int64
	pushed         int64
	popped         int64
	stolen         int64
	misses         int64
	releases       int64
	reclaims       int64
}

func newNaive(cap, sm, rv, f int64) *naiveDeque {
	return &naiveDeque{cap: cap, sm: sm, rv: rv, f: f, buf: make([]int64, cap)}
}

func (n *naiveDeque) releaseCheck() {
	if !n.fl {
		return
	}
	private := n.b - n.s
	shared := n.s - n.t
	r := private / 2
	if n.dm > r {
		r = n.dm
	}
	if v := n.sm - shared; v < r {
		r = v
	}
	if v := private - n.rv; v < r {
		r = v
	}
	if r < 0 {
		r = 0
	}
	if r >= 1 {
		n.s += r
		n.fl = false
		n.ag = 0
		n.dm = 0
		n.releases++
		return
	}
	n.ag++
	if n.ag == n.f {
		n.fl = false
		n.ag = 0
		n.dm = 0
	}
}

func (n *naiveDeque) push(x int64) string {
	if n.b-n.t == n.cap {
		return "reject:full"
	}
	n.buf[n.b%n.cap] = x
	n.b++
	n.pushed++
	n.releaseCheck()
	return "ok"
}

func (n *naiveDeque) pop() (int64, bool) {
	n.releaseCheck()
	if n.b-n.s > 0 {
		x := n.buf[(n.b-1)%n.cap]
		n.b--
		n.popped++
		return x, true
	}
	if n.s-n.t == 0 {
		return 0, false
	}
	g := (n.s - n.t + 1) / 2
	n.s -= g
	n.reclaims++
	x := n.buf[(n.b-1)%n.cap]
	n.b--
	n.popped++
	return x, true
}

func (n *naiveDeque) steal(m int64) ([]int64, string) {
	if m < 1 || m > n.cap {
		return nil, "reject:arg"
	}
	shared := n.s - n.t
	k := m
	if k > shared {
		k = shared
	}
	out := make([]int64, k)
	for i := int64(0); i < k; i++ {
		out[i] = n.buf[(n.t+i)%n.cap]
	}
	n.t += k
	n.stolen += k
	if k < m {
		deficit := m - k
		n.misses++
		if !n.fl {
			n.fl = true
			n.dm = deficit
		} else if deficit > n.dm {
			n.dm = deficit
		}
		n.ag = 0
	}
	return out, "ok"
}

type opKind int

const (
	opPush opKind = iota
	opPop
	opSteal
)

type testOp struct {
	kind opKind
	arg  int64
}

func TestNaiveDifferential(t *testing.T) {
	const groups = 2000
	rng := rand.New(rand.NewSource(20261002))
	for g := 0; g < groups; g++ {
		cap := int64(1 + rng.Intn(32))
		sm := int64(1 + rng.Intn(int(cap)))
		rv := int64(rng.Intn(int(cap) + 1))
		f := int64(1 + rng.Intn(6))
		d, err := New(cap, sm, rv, f)
		if err != nil {
			t.Fatalf("group %d: New(%d,%d,%d,%d): %v", g, cap, sm, rv, f, err)
		}
		nv := newNaive(cap, sm, rv, f)

		ops := make([]testOp, 60+rng.Intn(140))
		var seq int64
		for i := range ops {
			switch rng.Intn(3) {
			case 0:
				seq++
				ops[i] = testOp{opPush, seq}
			case 1:
				ops[i] = testOp{opPop, 0}
			default:
				// Sometimes illegal m to exercise rejection; allowed m
				// spans 1..Cap, illegal covers 0 and Cap+1.
				m := int64(rng.Intn(int(cap) + 2))
				ops[i] = testOp{opSteal, m}
			}
		}

		log := fmt.Sprintf("group %d config Cap=%d Sm=%d Rv=%d F=%d\n", g, cap, sm, rv, f)
		for step, op := range ops {
			switch op.kind {
			case opPush:
				err := d.Push(op.arg)
				want := nv.push(op.arg)
				got := "ok"
				if err != nil {
					got = "reject:full"
				}
				log += fmt.Sprintf("step %d Push(%d) -> %s (want %s)\n", step, op.arg, got, want)
				if got != want {
					t.Fatalf("group %d step %d: Push(%d) = %s, want %s\n%s", g, step, op.arg, got, want, log)
				}
			case opPop:
				x, ok := d.Pop()
				wx, wok := nv.pop()
				log += fmt.Sprintf("step %d Pop() -> (%d,%v) (want (%d,%v))\n", step, x, ok, wx, wok)
				if ok != wok || (ok && x != wx) {
					t.Fatalf("group %d step %d: Pop = (%d,%v), want (%d,%v)\n%s", g, step, x, ok, wx, wok, log)
				}
			case opSteal:
				got, gerr := d.Steal(op.arg)
				want, wstatus := nv.steal(op.arg)
				gstatus := "ok"
				if gerr != nil {
					gstatus = "reject:arg"
				}
				log += fmt.Sprintf("step %d Steal(%d) -> %v,%s (want %v,%s)\n", step, op.arg, got, gstatus, want, wstatus)
				if gstatus != wstatus {
					t.Fatalf("group %d step %d: Steal(%d) status %s, want %s\n%s", g, step, op.arg, gstatus, wstatus, log)
				}
				if gstatus == "ok" && !eqSlice(got, want) {
					t.Fatalf("group %d step %d: Steal(%d) = %v, want %v\n%s", g, step, op.arg, got, want, log)
				}
			}
			assertMatchNaive(t, g, step, d, nv, log)
		}
		// Final full drain through both regions, compared with the model.
		for {
			x, ok := d.Pop()
			wx, wok := nv.pop()
			if ok != wok || (ok && x != wx) {
				t.Fatalf("group %d drain: Pop (%d,%v) vs (%d,%v)\n%s", g, x, ok, wx, wok, log)
			}
			assertMatchNaive(t, g, -1, d, nv, log)
			if !ok {
				break
			}
		}
		// Log the whole input/output/judgment trace for the group.
		t.Logf("%sfinal: t=%d s=%d b=%d fl=%v ag=%d dm=%d stats={pushed=%d popped=%d stolen=%d misses=%d releases=%d reclaims=%d}",
			log, d.t, d.s, d.b, d.fl, d.ag, d.dm,
			nv.pushed, nv.popped, nv.stolen, nv.misses, nv.releases, nv.reclaims)
	}
}

func assertMatchNaive(t *testing.T, group, step int, d *SplitDeque, n *naiveDeque, log string) {
	t.Helper()
	fail := func(format string, args ...any) {
		t.Helper()
		t.Fatalf("group %d step %d: %s\n%s", group, step, fmt.Sprintf(format, args...), log)
	}
	if d.t != n.t || d.s != n.s || d.b != n.b {
		fail("t,s,b = %d,%d,%d want %d,%d,%d", d.t, d.s, d.b, n.t, n.s, n.b)
	}
	if !(d.t <= d.s && d.s <= d.b) {
		fail("invariant t<=s<=b violated: %d,%d,%d", d.t, d.s, d.b)
	}
	if d.s-d.t > d.sm {
		fail("|S|=%d exceeds Sm=%d", d.s-d.t, d.sm)
	}
	if d.fl != n.fl || d.ag != n.ag || d.dm != n.dm {
		fail("fl,ag,dm = %v,%d,%d want %v,%d,%d", d.fl, d.ag, d.dm, n.fl, n.ag, n.dm)
	}
	if d.stats.Pushed != n.pushed || d.stats.Popped != n.popped ||
		d.stats.Stolen != n.stolen || d.stats.Misses != n.misses ||
		d.stats.Releases != n.releases || d.stats.Reclaims != n.reclaims {
		fail("stats = %+v want pushed=%d popped=%d stolen=%d misses=%d releases=%d reclaims=%d",
			d.stats, n.pushed, n.popped, n.stolen, n.misses, n.releases, n.reclaims)
	}
	if d.stats.Pushed != d.stats.Popped+d.stats.Stolen+(d.b-d.t) {
		fail("identity pushed=popped+stolen+occupancy violated: %+v b-t=%d", d.stats, d.b-d.t)
	}
	// Element identity check at every live index (both ring models).
	for i := d.t; i < d.b; i++ {
		if d.buf[i%d.cap] != n.buf[i%n.cap] {
			fail("element at index %d: ring=%d model=%d", i, d.buf[i%d.cap], n.buf[i])
		}
	}
}
