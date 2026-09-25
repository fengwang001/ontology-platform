package check

import (
	"errors"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"ontology/bit"
)

type tc struct {
	name                       string
	n, ai, pi, l, r            int
	delta, wantP, wantR        int64
	errNew, errAdd, errP, errR error
}

var cs = []tc{
	{"zero-size", 0, 0, -1, 0, 0, 1, 0, 0, nil, bit.ErrBadIndex, nil, bit.ErrBadIndex},
	{"one-element", 1, 0, 0, 0, 0, 5, 5, 5, nil, nil, nil, nil},
	{"prefix-minus-one", 3, 1, -1, 1, 1, 2, 0, 2, nil, nil, nil, nil},
	{"negative-index", 3, -1, 0, 0, 0, 1, 0, 0, nil, bit.ErrBadIndex, nil, nil},
	{"index-too-big", 3, 3, 2, 0, 2, 1, 0, 0, nil, bit.ErrBadIndex, nil, nil},
	{"bad-range", 3, 0, 2, 2, 1, 1, 1, 0, nil, nil, nil, bit.ErrBadRange},
	{"new-negative", -1, 0, -1, 0, 0, 0, 0, 0, bit.ErrBadSize, nil, nil, nil},
}

func TestSemantics(t *testing.T) {
	for _, c := range cs {
		t.Run(c.name, func(t *testing.T) {
			tr, errN := bit.New(c.n)
			if !errors.Is(errN, c.errNew) {
				t.Fatalf("New=%v want %v", errN, c.errNew)
			}
			if c.errNew != nil {
				return
			}
			if err := tr.Add(c.ai, c.delta); !errors.Is(err, c.errAdd) {
				t.Fatalf("Add=%v want %v", err, c.errAdd)
			}
			g, err := tr.PrefixSum(c.pi)
			if !errors.Is(err, c.errP) || g != c.wantP {
				t.Fatalf("PrefixSum=%d,%v want %d,%v", g, err, c.wantP, c.errP)
			}
			g, err = tr.RangeSum(c.l, c.r)
			if !errors.Is(err, c.errR) || g != c.wantR {
				t.Fatalf("RangeSum=%d,%v want %d,%v", g, err, c.wantR, c.errR)
			}
		})
	}
}

func TestMatchesNaive(t *testing.T) {
	const n, ops = 1000, 10000
	tr, _ := bit.New(n)
	ref := NewNaive(n)
	rng := rand.New(rand.NewPCG(1, 2))
	for k := 0; k < ops; k++ {
		i, d := rng.IntN(n), int64(rng.IntN(7)-3)
		tr.Add(i, d)
		ref.Add(i, d)
	}
	for i := -1; i < n; i++ {
		g, _ := tr.PrefixSum(i)
		w, _ := ref.PrefixSum(i)
		if g != w {
			t.Fatalf("PrefixSum(%d)=%d naive=%d", i, g, w)
		}
	}
	for _, q := range [][2]int{{0, n - 1}, {n / 3, 2 * n / 3}, {0, 0}} {
		g, _ := tr.RangeSum(q[0], q[1])
		w, _ := ref.RangeSum(q[0], q[1])
		if g != w {
			t.Fatalf("RangeSum(%v)=%d naive=%d", q, g, w)
		}
	}
}

// addZeroBased is the buggy unshifted update: lowbit(0)==0 so j never moves.
func addZeroBased(a []int64, n, i int, done chan<- struct{}) {
	for j := i; j <= n; j += j & -j {
		a[j]++
	}
	done <- struct{}{}
}

func TestZeroBasedSpins(t *testing.T) {
	done := make(chan struct{}, 1)
	go addZeroBased(make([]int64, 6), 5, 0, done)
	select {
	case <-done:
		t.Fatal("zero-based update returned; expected spin")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNodeBound(t *testing.T) {
	const n = 100000
	tr, _ := bit.New(n)
	bound := int64(math.Log2(n) + 2)
	for k := 0; k < 1000; k++ {
		i := (k*7919 + 3) % n
		tr.Add(i, 1)
		if tr.LastNodes() > bound {
			t.Fatalf("Add nodes=%d > %d", tr.LastNodes(), bound)
		}
		tr.PrefixSum(i)
		if tr.LastNodes() > bound {
			t.Fatalf("PrefixSum nodes=%d > %d", tr.LastNodes(), bound)
		}
	}
}

func TestConcurrentReads(t *testing.T) {
	const n, g = 1000, 16
	tr, _ := bit.New(n)
	for i := 0; i < n; i++ {
		tr.Add(i, int64(i))
	}
	want, _ := tr.PrefixSum(n - 1)
	res := make(chan int64, g)
	for k := 0; k < g; k++ {
		go func() {
			v, _ := tr.PrefixSum(n - 1)
			res <- v
		}()
	}
	for k := 0; k < g; k++ {
		if v := <-res; v != want {
			t.Fatalf("read=%d want %d", v, want)
		}
	}
}
