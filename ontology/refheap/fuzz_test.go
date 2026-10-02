package refheap

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func runRandomCase(t *testing.T, seed int64, steps int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))

	c := int64(60 + rng.Intn(400))
	u := int64(1 + rng.Intn(30))
	m := int64(rng.Intn(20))
	h, err := NewHeap(c, u, m)
	if err != nil {
		t.Fatal(err)
	}
	n := newNaive(c, u, m)
	g := &generator{rng: rng, heap: h, sim: n}

	var log []string
	log = append(log, "INIT C=%d U=%d M=%d seed=%d")
	log[0] = formatInit(c, u, m, seed)

	failf := func(msg string) {
		t.Fatalf("seed=%d\n%s\n%s", seed, msg, joinLog(log))
	}

	for s := 0; s < steps; s++ {
		operation := g.oneOp()
		_, out := operation.run(h, n)
		g.now = h.maxNow
		log = append(log, operation.line+" => "+out)
		if verboseNaive {
			t.Logf("[seed %d] %s => %s", seed, operation.line, out)
		}

		// Full state equivalence after every operation.
		hs, ns := snapHeap(h), snapNaive(n)
		if !snapshotsEqual(hs, ns) {
			failf("state divergence\nh=" + dumpSnap(hs) + "\nn=" + dumpSnap(ns))
		}

		// Invariants.
		var sum int64
		for _, ob := range h.objs {
			sum += ob.size
			if ob.isRef && ob.target != 0 {
				if _, ok := h.objs[ob.target]; !ok {
					failf("dangling referent after op: " + operation.line)
				}
			}
		}
		if sum != h.used || h.used > h.capacity {
			failf(fmt.Sprintf("used invariant broken: sum=%d used=%d cap=%d", sum, h.used, h.capacity))
		}

	}
}

func TestRandomDifferential2000(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		runRandomCase(t, seed, 80)
	}
}

// Immediately after each Collect, the one-time visit counter equals the
// number of surviving objects.
func TestMarkVisitCountAfterCollect(t *testing.T) {
	for seed := int64(9000); seed < 9050; seed++ {
		rng := rand.New(rand.NewSource(seed))
		c := int64(80 + rng.Intn(300))
		h, _ := NewHeap(c, int64(1+rng.Intn(20)), int64(rng.Intn(10)))
		n := newNaive(c, h.softUnit, h.softKeep)
		g := &generator{rng: rng, heap: h, sim: n}
		var log []string
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("seed %d: %v\n%s", seed, r, joinLog(log))
			}
		}()
		for s := 0; s < 120; s++ {
			o := g.oneOp()
			accept, _ := o.run(h, n)
			g.now = h.maxNow
			log = append(log, fmt.Sprintf("%s accept=%v", o.line, accept))
			if accept && strings.HasPrefix(o.line, "Collect(") {
				if h.LastMarkVisitCount() != len(h.objs) {
					t.Fatalf("seed %d: mark visits %d != survivors %d\n%s",
						seed, h.LastMarkVisitCount(), len(h.objs), joinLog(log))
				}
			}
			g.gcLists(0)
		}
	}
}

// Same operation sequence replays to identical results (deterministic public
// behavior; the random replay above already asserts this across two models).
func TestDeterministicReplay(t *testing.T) {
	script := func(h *Heap) []int64 {
		var got []int64
		q := h.CreateQueue()
		a := mustOK(h.Alloc(20, 1, true))
		b := mustOK(h.Alloc(10, 0, false))
		mustOKInt(h.SetField(a, 0, b))
		r := mustOK(h.NewRef(Soft, a, q, 5, 0))
		mustOKInt(h.SetRoot(r))
		res1 := mustOKResult(h.Collect(3))
		got = append(got, res1.Reclaimed...)
		got = append(got, res1.Finalized...)
		p, _ := h.Poll(q)
		got = append(got, p)
		res2 := mustOKResult(h.Collect(9))
		got = append(got, res2.Reclaimed...)
		got = append(got, res2.CatchAll...)
		got = append(got, res2.Used)
		return got
	}
	h1, _ := NewHeap(100, 10, 5)
	h2, _ := NewHeap(100, 10, 5)
	first := script(h1)
	second := script(h2)
	if !intsEqual(first, second) {
		t.Fatalf("replay differs: %v vs %v", first, second)
	}
}
