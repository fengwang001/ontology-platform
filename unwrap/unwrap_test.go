package unwrap

import (
	"errors"
	"math/rand"
	"ontology/sar"
	"sync"
	"testing"
)

// refFeed is the independent hand-derived oracle (all residues are < M).
func refFeed(n int, st []uint64) (abs []int64, es []error, last int64) {
	a, _ := sar.New(n)
	have := false
	for _, s := range st {
		var e error
		if !have {
			last, have = int64(s), true
		} else {
			d := (s - uint64(last)&a.Mask()) & a.Mask()
			switch a.Classify(d) {
			case sar.Less:
				last += int64(d)
			case sar.Incomparable:
				e = sar.ErrIncomparable
			case sar.Greater:
				e = sar.ErrGreater
			}
		}
		v := last
		if e != nil {
			v = 0
		}
		abs, es = append(abs, v), append(es, e)
	}
	return
}
func TestFeedTable(t *testing.T) { // section 3 trace, N=4
	u, _ := New(4)
	wA := []int64{13, 14, 15, 16, 17, 18, 0, 19}
	wE := []bool{false, false, false, false, false, false, true, false}
	for i, s := range []uint64{13, 14, 15, 0, 1, 2, 10, 3} {
		if v, e := u.Feed(s); (e != nil) != wE[i] || v != wA[i] {
			t.Fatalf("step %d Feed(%d)=(%d,%v) want (%d,%v)", i, s, v, e, wA[i], wE[i])
		}
	}
}
func TestReferenceModel(t *testing.T) { // invariant 1: random streams vs oracle
	for _, n := range []int{1, 4, 11, 20} {
		a0, _ := sar.New(n)
		rng := rand.New(rand.NewSource(int64(n)))
		for tr := 0; tr < 10; tr++ {
			st, cur := make([]uint64, 128), rng.Uint64()&a0.Mask()
			for i := range st {
				st[i] = cur
				cur = (cur + rng.Uint64()&a0.Mask()) & a0.Mask()
			}
			wA, wE, wL := refFeed(n, st)
			u, _ := New(n)
			for i, s := range st {
				v, e := u.Feed(s)
				if !errors.Is(e, wE[i]) || (e == nil && v != wA[i]) || (i == len(st)-1 && func() bool { l, _ := u.Last(); return l != wL }()) {
					t.Fatalf("n=%d tr=%d i=%d (%d,%v) want (%d,%v)", n, tr, i, v, e, wA[i], wE[i])
				}
			}
		}
	}
}
func TestFeedMonotonic(t *testing.T) { // invariant 3: +1 stream's absolutes are exactly i
	for _, n := range []int{2, 4, 16, 63} {
		u, _ := New(n)
		for i := uint64(0); i < 400; i++ {
			if v, e := u.Feed(i & (u.a.Mod() - 1)); e != nil || v != int64(i) {
				t.Fatalf("n=%d i=%d (%d,%v)", n, i, v, e)
			}
		}
	}
}
func TestRejectLeavesLast(t *testing.T) { // invariant 4 + four distinct sentinels
	E := [...]error{sar.ErrWidth, sar.ErrOutOfRange, sar.ErrIncomparable, sar.ErrGreater}
	if errors.Is(E[0], E[1]) || errors.Is(E[0], E[2]) || errors.Is(E[0], E[3]) || errors.Is(E[1], E[2]) || errors.Is(E[1], E[3]) || errors.Is(E[2], E[3]) {
		t.Fatal("the four sentinels must be mutually distinct")
	}
	if _, e := New(0); !errors.Is(e, sar.ErrWidth) {
		t.Fatalf("New(0)=%v", e)
	}
	kind := []error{sar.ErrOutOfRange, sar.ErrIncomparable, sar.ErrGreater}
	u, _ := New(4)
	base, _ := u.Feed(0)
	for _, p := range [][2]uint64{{16, 0}, {8, 1}, {15, 2}} {
		if _, e := u.Feed(p[0]); !errors.Is(e, kind[p[1]]) {
			t.Fatalf("Feed(%d)=%v kind %d", p[0], e, p[1])
		}
		if l, h := u.Last(); !h || l != 0 {
			t.Fatalf("reject %d left (%d,%v)", p[0], l, h)
		}
	}
	if v, e := u.Feed(1); e != nil || v != base+1 {
		t.Fatalf("recovery (%d,%v)", v, e)
	}
}
func TestFeedChecksO1(t *testing.T) { // one probe/Feed; total exactly m, not m^2/2
	for _, n := range []int{1, 2, 4, 8, 16} {
		for _, m := range []int{100, 500, 1000, 5000, 10000} {
			u, _ := New(n)
			for i := 0; i < m; i++ {
				_, e := u.Feed(uint64(i) & u.a.Mask())
				rej := n == 1 && i%2 == 1
				if (rej && !errors.Is(e, sar.ErrIncomparable)) || (!rej && e != nil) || u.checksLast > 1 {
					t.Fatalf("n=%d i=%d e=%v probes=%d", n, i, e, u.checksLast)
				}
			}
			if u.checksTotal != uint64(m) {
				t.Fatalf("n=%d m=%d total=%d want %d", n, m, u.checksTotal, m)
			}
		}
	}
}
func TestConcurrentReaders(t *testing.T) { // race-clean identical snapshots, no sleep
	u, _ := New(13)
	for i := 0; i < 300; i++ {
		u.Feed(uint64(i) & u.a.Mask())
	}
	want, _ := u.Last()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 2000; k++ {
				l, h := u.Last()
				if l != want || !h || u.Width() != 13 {
					t.Errorf("snapshot (%d,%v,%d)", l, h, u.Width())
				}
				u.Cmp(uint64(k)&u.a.Mask(), 0)
			}
		}()
	}
	wg.Add(1)
	go func() { defer wg.Done(); _ = u.SelfCheck() }()
	wg.Wait()
}
func TestSelfCheckAcrossWidths(t *testing.T) {
	for _, n := range []int{1, 2, 4, 16, 63} {
		u, _ := New(n)
		if err := u.SelfCheck(); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
	}
}
