package heap

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// model abstracts the real heap and the naive reference
// implementation so the differential driver can treat them alike.
type model interface {
	Alloc(s int64, f int, fin bool) (int, error)
	NewRef(k Kind, target, q int, s, now int64) (int, error)
	CreateQueue() int
	SetField(o, i, t int) error
	SetRoot(o int) error
	ClearRoot(o int) error
	Get(r int, now int64) (int, error)
	Poll(q int) (int, error)
	Finalize(n int) ([]int, error)
	Collect(now int64) (CollectResult, error)
}

type opKind int

const (
	opAlloc opKind = iota
	opNewRef
	opCreateQueue
	opSetField
	opSetRoot
	opClearRoot
	opGet
	opPoll
	opFinalize
	opCollect
)

type testOp struct {
	kind opKind
	desc string
	a, b int
	c    int
	s    int64
	now  int64
	k    Kind
	fin  bool
}

type opOut struct {
	str         string
	id          int   // object id produced by a successful Alloc/NewRef
	q           int   // queue number produced by CreateQueue
	acceptedNow int64 // now accepted by this op, -1 otherwise
}

func runOp(m model, op testOp) opOut {
	switch op.kind {
	case opAlloc:
		id, err := m.Alloc(op.s, op.a, op.fin)
		return opOut{str: fmt.Sprintf("id=%d err=%v", id, err), id: id, acceptedNow: -1}
	case opNewRef:
		id, err := m.NewRef(op.k, op.a, op.b, op.s, op.now)
		out := opOut{str: fmt.Sprintf("id=%d err=%v", id, err), id: id, acceptedNow: -1}
		if err == nil {
			out.acceptedNow = op.now
		}
		return out
	case opCreateQueue:
		q := m.CreateQueue()
		return opOut{str: fmt.Sprintf("q=%d", q), q: q, acceptedNow: -1}
	case opSetField:
		err := m.SetField(op.a, op.b, op.c)
		return opOut{str: fmt.Sprintf("err=%v", err), acceptedNow: -1}
	case opSetRoot:
		err := m.SetRoot(op.a)
		return opOut{str: fmt.Sprintf("err=%v", err), acceptedNow: -1}
	case opClearRoot:
		err := m.ClearRoot(op.a)
		return opOut{str: fmt.Sprintf("err=%v", err), acceptedNow: -1}
	case opGet:
		tgt, err := m.Get(op.a, op.now)
		out := opOut{str: fmt.Sprintf("target=%d err=%v", tgt, err), acceptedNow: -1}
		if err == nil {
			out.acceptedNow = op.now
		}
		return out
	case opPoll:
		id, err := m.Poll(op.a)
		return opOut{str: fmt.Sprintf("id=%d err=%v", id, err), acceptedNow: -1}
	case opFinalize:
		ids, err := m.Finalize(op.a)
		return opOut{str: fmt.Sprintf("ids=%v err=%v", ids, err), acceptedNow: -1}
	case opCollect:
		res, err := m.Collect(op.now)
		out := opOut{str: fmt.Sprintf("res=%+v err=%v", res, err), acceptedNow: -1}
		if err == nil {
			out.acceptedNow = op.now
		}
		return out
	}
	panic("unknown op")
}

type gen struct {
	rng  *rand.Rand
	ids  []int
	qs   []int
	now  int64
	next int // predicted next object id, for harmless bad-id guesses
}

func (g *gen) pickID() int {
	if len(g.ids) == 0 || g.rng.Intn(4) == 0 {
		return 0
	}
	return g.ids[g.rng.Intn(len(g.ids))]
}

func (g *gen) pickQueue() int {
	if len(g.qs) == 0 || g.rng.Intn(2) == 0 {
		return 0
	}
	return g.qs[g.rng.Intn(len(g.qs))]
}

func (g *gen) badID() int { return g.next + 1000 + g.rng.Intn(1000) }

func (g *gen) advanceNow(step int64) int64 {
	g.now += g.rng.Int63n(step + 1)
	return g.now
}

func (g *gen) nextOp() testOp {
	switch g.rng.Intn(100) {
	case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19: // Alloc 20%
		s := int64(1 + g.rng.Intn(30))
		f := g.rng.Intn(4)
		fin := g.rng.Intn(4) == 0
		return testOp{kind: opAlloc, s: s, a: f, fin: fin,
			desc: fmt.Sprintf("Alloc(%d,%d,%v)", s, f, fin)}
	case 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34: // NewRef 15%
		k := Kind(g.rng.Intn(3))
		target := g.pickID()
		q := g.pickQueue()
		s := int64(1 + g.rng.Intn(10))
		now := g.advanceNow(3)
		return testOp{kind: opNewRef, k: k, a: target, b: q, s: s, now: now,
			desc: fmt.Sprintf("NewRef(%d,%d,%d,%d,%d)", k, target, q, s, now)}
	case 35, 36, 37, 38: // CreateQueue 4%
		return testOp{kind: opCreateQueue, desc: "CreateQueue()"}
	case 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50: // SetField 12%
		o := g.pickID()
		i := g.rng.Intn(4)
		tgt := g.pickID()
		return testOp{kind: opSetField, a: o, b: i, c: tgt,
			desc: fmt.Sprintf("SetField(%d,%d,%d)", o, i, tgt)}
	case 51, 52, 53, 54, 55, 56, 57, 58: // SetRoot 8%
		o := g.pickID()
		return testOp{kind: opSetRoot, a: o, desc: fmt.Sprintf("SetRoot(%d)", o)}
	case 59, 60, 61, 62, 63: // ClearRoot 5%
		o := g.pickID()
		return testOp{kind: opClearRoot, a: o, desc: fmt.Sprintf("ClearRoot(%d)", o)}
	case 64, 65, 66, 67, 68, 69, 70, 71: // Get 8%
		r := g.pickID()
		now := g.advanceNow(2)
		return testOp{kind: opGet, a: r, now: now, desc: fmt.Sprintf("Get(%d,%d)", r, now)}
	case 72, 73, 74, 75, 76: // Poll 5%
		q := g.pickQueue()
		if q == 0 && len(g.qs) > 0 {
			q = g.qs[0]
		}
		return testOp{kind: opPoll, a: q, desc: fmt.Sprintf("Poll(%d)", q)}
	case 77, 78, 79, 80, 81: // Finalize 5%
		n := g.rng.Intn(6)
		return testOp{kind: opFinalize, a: n, desc: fmt.Sprintf("Finalize(%d)", n)}
	default: // Collect 18%
		now := g.advanceNow(30)
		return testOp{kind: opCollect, now: now, desc: fmt.Sprintf("Collect(%d)", now)}
	}
}

// corrupt randomly breaks one parameter so rejection paths are
// exercised too; both implementations must reject identically.
func (g *gen) corrupt(op testOp) testOp {
	op.desc += " [corrupted:"
	switch g.rng.Intn(8) {
	case 0:
		op.now = g.now - 1 - g.rng.Int63n(5)
		op.desc += fmt.Sprintf(" now=%d", op.now)
	case 1:
		op.a = g.badID()
		op.desc += fmt.Sprintf(" a=%d", op.a)
	case 2:
		op.k = Kind(3 + g.rng.Intn(3))
		op.desc += fmt.Sprintf(" kind=%d", op.k)
	case 3:
		op.s = int64([]int64{0, -1, 1_000_001}[g.rng.Intn(3)])
		op.desc += fmt.Sprintf(" s=%d", op.s)
	case 4:
		op.b = 8 + g.rng.Intn(3)
		op.desc += fmt.Sprintf(" b=%d", op.b)
	case 5:
		op.a = []int{-1, 1_000_001}[g.rng.Intn(2)]
		op.desc += fmt.Sprintf(" a=%d", op.a)
	case 6:
		op.now = 1_000_000_000_000_001
		op.desc += " now=1e15+1"
	case 7:
		op.c = g.badID()
		op.desc += fmt.Sprintf(" c=%d", op.c)
	}
	op.desc += "]"
	return op
}

// TestRandomDifferential replays 2000 random heaps and operation
// sequences against two independent real heaps (replay determinism)
// and the naive set-based reference implementation.
func TestRandomDifferential(t *testing.T) {
	const groups = 2000
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(g)))
		C := 30 + rng.Int63n(270)
		U := 1 + rng.Int63n(50)
		M := rng.Int63n(21)
		h1, err := New(C, U, M)
		if err != nil {
			t.Fatal(err)
		}
		h2, err := New(C, U, M)
		if err != nil {
			t.Fatal(err)
		}
		nv := newNaive(C, U, M)
		gn := &gen{rng: rng}
		ops := 80 + rng.Intn(80)

		var trace strings.Builder
		verbose := g == 0 // log the full trace of the first group
		fail := func(i int, op testOp, o1, o2, on opOut) {
			t.Errorf("group %d op %d %s:\n  heap1: %s\n  heap2: %s\n  naive: %s",
				g, i, op.desc, o1.str, o2.str, on.str)
			t.Errorf("group %d params: C=%d U=%d M=%d; trace so far:\n%s", g, C, U, M, trace.String())
		}

		for i := 0; i < ops; i++ {
			op := gn.nextOp()
			if gn.rng.Intn(100) < 6 {
				op = gn.corrupt(op)
			}
			o1 := runOp(h1, op)
			o2 := runOp(h2, op)
			on := runOp(nv, op)
			fmt.Fprintf(&trace, "op %d: %s => %s\n", i, op.desc, o1.str)
			if verbose {
				t.Logf("group 0 op %d: %s => %s (判定: heap1==heap2==naive)", i, op.desc, o1.str)
			}
			if o1.str != o2.str || o1.str != on.str {
				fail(i, op, o1, o2, on)
				break
			}
			// The three outputs agree; update generator bookkeeping.
			if o1.id != 0 {
				gn.ids = append(gn.ids, o1.id)
			}
			if o1.q != 0 {
				gn.qs = append(gn.qs, o1.q)
			}
			if o1.acceptedNow >= 0 && o1.acceptedNow > gn.now {
				gn.now = o1.acceptedNow
			}
			gn.next++
			if op.kind == opCollect && o1.acceptedNow >= 0 {
				if h1.visits != len(h1.objects) {
					t.Fatalf("group %d op %d: visits %d != survivors %d",
						g, i, h1.visits, len(h1.objects))
				}
				checkInvariants(t, h1)
			}
		}
		if g%200 == 0 {
			t.Logf("group %d done: C=%d U=%d M=%d ops=%d (判定: 三实现输出逐操作一致)", g, C, U, M, ops)
		}
		if t.Failed() {
			t.Fatalf("stopping at group %d", g)
		}
	}
}

// TestConcurrentSmoke hammers one shared heap from many goroutines.
// Run with -race; every result must be equivalent to some serial
// order and all invariants must hold afterwards.
func TestConcurrentSmoke(t *testing.T) {
	h := newHeap(t, 100_000, 10, 5)
	const workers = 8
	const opsEach = 400
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			var ids []int
			var qs []int
			var now int64
			pick := func() int {
				if len(ids) == 0 || rng.Intn(3) == 0 {
					return 0
				}
				return ids[rng.Intn(len(ids))]
			}
			for i := 0; i < opsEach; i++ {
				now += rng.Int63n(3)
				switch rng.Intn(10) {
				case 0, 1:
					id, err := h.Alloc(int64(1+rng.Intn(30)), rng.Intn(4), rng.Intn(4) == 0)
					if err == nil {
						ids = append(ids, id)
					}
				case 2, 3:
					q := 0
					if len(qs) > 0 && rng.Intn(2) == 0 {
						q = qs[rng.Intn(len(qs))]
					}
					id, err := h.NewRef(Kind(rng.Intn(3)), pick(), q, int64(1+rng.Intn(10)), now)
					if err == nil {
						ids = append(ids, id)
					}
				case 4:
					qs = append(qs, h.CreateQueue())
				case 5:
					_ = h.SetField(pick(), rng.Intn(4), pick())
				case 6:
					_ = h.SetRoot(pick())
				case 7:
					_, _ = h.Get(pick(), now)
				case 8:
					_, _ = h.Finalize(rng.Intn(4))
				case 9:
					_, _ = h.Collect(now)
				}
			}
		}(int64(w) + 1)
	}
	wg.Wait()
	if _, err := h.Collect(1_000_000_000); err != nil {
		t.Fatalf("final Collect: %v", err)
	}
	checkInvariants(t, h)
	if h.visits != len(h.objects) {
		t.Fatalf("visits %d != survivors %d", h.visits, len(h.objects))
	}
}
