package api

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/cbuf"
	_ "unsafe"
)

//go:linkname probeCount ontology/cbuf.probeCount
func probeCount(*cbuf.Buffer) int64

func eqb(xs []Msg, rb map[[2]int64]Msg) bool {
	for _, x := range xs {
		if _, ok := rb[mk(x)]; !ok {
			return false
		}
	}
	return len(xs) == len(rb)
}
func scenario(rng *rand.Rand, n, c, e int) []Msg {
	pool := closed(rng, n, c)
	arr := append([]Msg{}, pool...)
	for k := 0; k < e; k++ {
		arr = append(arr, pool[rng.Intn(c)])
	}
	rng.Shuffle(len(arr), func(i, j int) { arr[i], arr[j] = arr[j], arr[i] })
	return arr
}
func TestNaiveReferenceEquivalence(t *testing.T) {
	for seed := int64(0); seed < 3; seed++ {
		for _, n := range []int{2, 3} {
			for _, cap := range []int{0, 2, 64} {
				rng := rand.New(rand.NewSource(seed*100 + int64(n*7+cap)))
				real, ref := must(New(n, cap)), newNaive(n, cap)
				for _, x := range scenario(rng, n, 40, 12) {
					g1, e1 := real.Receive(x)
					rg, e2 := ref.recv(x)
					if (e1 == nil) != (e2 == nil) || len(g1) != rg || !veq(real.Local(), ref.l) || real.Dups() != ref.dup || !eqb(real.Buffered(), ref.b) || len(real.Delivered()) != len(ref.d) {
						t.Fatalf("seed=%d n=%d cap=%d divergence after %v", seed, n, cap, x)
					}
				}
			}
		}
	}
}
func TestCausalOrderInvariant(t *testing.T) {
	for seed := int64(0); seed < 3; seed++ {
		rng := rand.New(rand.NewSource(seed))
		b, _ := New(4, 64)
		for _, x := range scenario(rng, 4, 60, 0) {
			if _, e := b.Receive(x); e != nil {
				t.Fatal(e)
			}
		}
		if e := ordered(b.Delivered()); e != nil {
			t.Fatal(e)
		}
	}
}
func TestClosedSetCompleteness(t *testing.T) {
	for ci, tc := range []struct{ n, c, e int }{{2, 30, 10}, {3, 50, 20}, {5, 80, 0}, {4, 60, 25}} {
		b, _ := New(tc.n, 200)
		for _, x := range scenario(rand.New(rand.NewSource(int64(ci+99))), tc.n, tc.c, tc.e) {
			if _, e := b.Receive(x); e != nil {
				t.Fatal(e)
			}
		}
		seen := map[[2]int64]bool{}
		for _, x := range b.Delivered() {
			seen[mk(x)] = true
		}
		if len(b.Buffered()) != 0 || len(b.Delivered()) != tc.c || b.Dups() != int64(tc.e) || len(seen) != tc.c {
			t.Fatalf("case %d: buf=%d del=%d dups=%d uniq=%d", ci, len(b.Buffered()), len(b.Delivered()), b.Dups(), len(seen))
		}
	}
}
func TestRejectionLeavesNoTrace(t *testing.T) {
	for _, c := range []struct{ n, mb int }{{0, 4}, {2, -1}} {
		if _, e := New(c.n, c.mb); e != ErrBadParams {
			t.Fatalf("bad params n=%d mb=%d: %v", c.n, c.mb, e)
		}
	}
	if ErrBadParams == ErrBadSender || ErrBadSender == ErrBadVector || ErrBadVector == ErrBufferFull {
		t.Fatal("sentinels not distinct")
	}
	bads := []Msg{mm(3, 1, 0, 0), mm(0, 1, 0), mm(0, -1, 0, 0), mm(0, 0, 1, 1)}
	wants := []error{ErrBadSender, ErrBadVector, ErrBadVector, ErrBadVector}
	b, _ := New(3, 2)
	for i, x := range bads {
		s := snap(b)
		if _, e := b.Receive(x); !errors.Is(e, wants[i]) || snap(b) != s {
			t.Fatalf("case %d: %v left a trace", i, e)
		}
	}
	full, _ := New(1, 0)
	s := snap(full)
	_, fe := full.Receive(mm(0, 2))
	if !errors.Is(fe, ErrBufferFull) || snap(full) != s {
		t.Fatal("full-buffer reject left a trace")
	}
	if _, e := b.Receive(mm(0, 1, 0, 0)); e != nil {
		t.Fatalf("unusable after rejection: %v", e)
	}
}
func TestCheckCountNotLinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		cb, _ := cbuf.New(2, m+1)
		for seq := int64(2); seq <= int64(m)+1; seq++ {
			if _, e := cb.Receive(mm(1, 0, seq)); e != nil {
				t.Fatal(e)
			}
		}
		if _, e := cb.Receive(mm(0, 1, 0)); e != nil || probeCount(cb) > 4 {
			t.Fatalf("m=%d arrival not O(1): %d", m, probeCount(cb))
		}
		g, e := cb.Receive(mm(1, 0, 1))
		if e != nil || len(g) != m+1 || probeCount(cb) > int64((len(g)+1)*2+2) {
			t.Fatalf("m=%d cascade checks=%d del=%d", m, probeCount(cb), len(g))
		}
	}
}
func TestConcurrentDelivery(t *testing.T) {
	const n, total, extra, workers = 4, 40, 20, 8
	b, _ := New(n, total+extra)
	ch := make(chan Msg)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range ch {
				b.Receive(m)
			}
		}()
	}
	for _, x := range scenario(rand.New(rand.NewSource(7)), n, total, extra) {
		ch <- x
	}
	close(ch)
	wg.Wait()
	if len(b.Buffered()) != 0 || len(b.Delivered()) != total || b.Dups() != extra || ordered(b.Delivered()) != nil {
		t.Fatalf("buf=%d del=%d dups=%d", len(b.Buffered()), len(b.Delivered()), b.Dups())
	}
}
func must(x *Buffer, _ error) *Buffer { return x }
