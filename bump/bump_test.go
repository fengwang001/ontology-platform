package bump

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

func bumpsSum(a *Allocator) int {
	s := 0
	a.mu.Lock()
	for _, b := range a.bumps {
		s += b
	}
	a.mu.Unlock()
	return s
}

// TestInvariantNaiveReference pins invariant 2 on seeded random scripts.
func TestInvariantNaiveReference(t *testing.T) {
	for seed := int64(0); seed < 32; seed++ {
		rng := rand.New(rand.NewSource(seed))
		al := []int{1, 2, 4, 8, 16, 32}[rng.Intn(6)]
		size, max := al*(1+rng.Intn(8)), 1+rng.Intn(6)
		ops := make([]Op, 120)
		for i := range ops {
			if rng.Intn(7) == 0 {
				ops[i] = Op{Reset: true}
			} else {
				ops[i] = Op{N: -2 + rng.Intn(al*8+3)}
			}
		}
		a, _ := New(size, al, max)
		ref := NaiveSim(size, al, max, ops)
		ri := 0
		for _, op := range ops {
			if op.Reset {
				a.Reset()
				continue
			}
			off, gen, e := a.Alloc(op.N)
			if off != ref[ri].Off || gen != ref[ri].Gen || !errors.Is(e, ref[ri].Err) {
				t.Fatalf("seed=%d step=%d: got (%d,%d,%v), naive %+v", seed, ri, off, gen, e, ref[ri])
			}
			ri++
		}
	}
}

// TestRejectedOpsLeaveNoTrace pins invariant 4 on every rejection path.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	a, _ := New(16, 8, 2)
	a.Alloc(8)
	before := append([]int(nil), a.bumps...)
	cur, gen := a.cur, a.gen
	for _, n := range []int{0, -1, 17} {
		if _, _, e := a.Alloc(n); !errors.Is(e, ErrInvalidSize) &&
			!errors.Is(e, ErrArenaExhausted) {
			t.Fatalf("Alloc(%d) unexpected err %v", n, e)
		}
		if a.cur != cur || a.gen != gen {
			t.Fatalf("Alloc(%d) moved cur/gen", n)
		}
		for i := range before {
			if a.bumps[i] != before[i] {
				t.Fatalf("Alloc(%d) changed bump %d", n, i)
			}
		}
	}
	// Rejections left bump=8 in arena0; its 8-byte tail still serves Alloc(8).
	if off, _, e := a.Alloc(8); e != nil || off != 8 {
		t.Fatalf("allocator unusable after rejections: off=%d e=%v", off, e)
	}
	for _, cfg := range [][3]int{{8, 6, 1}, {4, 8, 1}, {16, 8, 0}} {
		if x, e := New(cfg[0], cfg[1], cfg[2]); e == nil || x != nil {
			t.Fatalf("New%v must fail and return nil", cfg)
		}
	}
}

// TestProbeNotLinearInArenas: after filling m arenas the final Alloc examines
// exactly one arena for every m; the current pointer is used, never a scan.
// probe is unexported: only this in-package white-box test may read it.
func TestProbeNotLinearInArenas(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		a, _ := New(8, 8, m+1)
		for i := 0; i < m; i++ {
			if _, _, e := a.Alloc(8); e != nil || a.probe != 1 {
				t.Fatalf("m=%d i=%d: e=%v probe=%d", m, i, e, a.probe)
			}
		}
		if off, _, e := a.Alloc(1); e != nil || off != m*8 || a.probe != 1 {
			t.Fatalf("m=%d final: off=%d e=%v probe=%d", m, off, e, a.probe)
		}
	}
}

// TestConcurrentAlloc: N goroutines allocate once; offsets distinct/aligned,
// bytes conserved, concurrent readers never see used bytes shrink. Timing is
// driven by closing a channel; no sleeps.
func TestConcurrentAlloc(t *testing.T) {
	const N = 128
	a, _ := New(8, 8, N)
	offs := make([]int, N)
	start, done := make(chan struct{}), make(chan struct{})
	var wg, rw sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; offs[i], _, _ = a.Alloc(3) }(i)
	}
	mono := true
	rw.Add(1)
	go func() {
		defer rw.Done()
		prev := 0
		for {
			select {
			case <-done:
				return
			default:
				if s := bumpsSum(a); s < prev {
					mono = false
				} else {
					prev = s
				}
			}
		}
	}()
	close(start)
	wg.Wait()
	close(done)
	rw.Wait()
	seen := map[int]bool{}
	for _, off := range offs {
		if off%8 != 0 || seen[off] {
			t.Fatalf("bad/dup offset %d", off)
		}
		seen[off] = true
	}
	if !mono || len(seen) != N || bumpsSum(a) != N*8 {
		t.Fatalf("mono=%v distinct=%d/%d sum=%d want %d", mono, len(seen), N, bumpsSum(a), N*8)
	}
}
