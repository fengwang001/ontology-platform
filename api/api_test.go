package api

import (
	"errors"
	"slices"
	"sync"
	"testing"
)

// eachSeq 对每个内置追加序列（含多档随机非单调序列）建实例并回调。
func eachSeq(t *testing.T, f func(*testing.T, *API, []int64)) {
	seqs := map[string][]int64{
		"spec10": {50, 40, 70, 60, 70, 65, 90, 80, 90, 85},
		"single": {7}, "decr": {99, 80, 60, 40, 20, 0},
	}
	for i, mod := range []int64{2, 17, 61} {
		seqs[string(rune('a'+i))] = lcgSeq(300, mod)
	}
	for n, s := range seqs {
		t.Run(n, func(t *testing.T) {
			a, _ := New(100, len(s)+10) // 序列构造即合法，无需查错
			a.Append(s)
			f(t, a, s)
		})
	}
}

// sweep 对 [最小TS-1, 最大TS+1] 内每个 t 查询并回调。
func sweep(a *API, seq []int64, f func(q, off int64, found bool)) {
	lo, hi := seq[0], seq[0]
	for _, v := range seq {
		lo, hi = min(lo, v), max(hi, v)
	}
	for q := lo - 1; q <= hi+1; q++ {
		off, found := a.Lookup(q)
		f(q, off, found)
	}
}

func TestNaiveConsistency(t *testing.T) {
	eachSeq(t, func(t *testing.T, a *API, seq []int64) {
		sweep(a, seq, func(q, off int64, found bool) {
			w, wf := naive(100, seq, q)
			if off != w || found != wf {
				t.Fatalf("Lookup(%d)=(%d,%v) want (%d,%v)", q, off, found, w, wf)
			}
		})
	})
}

func TestIndexStrict(t *testing.T) {
	eachSeq(t, func(t *testing.T, a *API, seq []int64) {
		runMax, pTS, pOff := int64(-1), int64(-1), int64(-1)
		for _, e := range a.lg.Index() {
			if e.TS <= pTS || e.Off <= pOff {
				t.Fatalf("not strictly increasing: %+v", e)
			}
			pTS, pOff = e.TS, e.Off
			if runMax = max(runMax, slices.Max(seq[:e.Off-100+1])); e.TS != runMax {
				t.Fatalf("entry %+v != running max %d", e, runMax)
			}
		}
	})
}

func TestQueryMonotonic(t *testing.T) {
	eachSeq(t, func(t *testing.T, a *API, seq []int64) {
		prev := int64(-1)
		sweep(a, seq, func(q, off int64, _ bool) {
			if prev >= 0 && off < prev {
				t.Fatalf("t=%d off=%d < prev=%d", q, off, prev)
			}
			prev = off
		})
	})
}

func TestFailureAtomic(t *testing.T) {
	cases := []struct {
		name  string
		batch []int64
		want  error
	}{
		{"negative ts", []int64{5, -1, 6}, ErrNegativeTS},
		{"over capacity", []int64{1, 2, 3, 4}, ErrCapacity},
	}
	for _, c := range cases {
		a, _ := New(100, 5) // 容量 5，已有 3 条
		a.Append([]int64{9, 1, 9})
		leo, n, snap := a.LEO(), len(a.lg.Index()), a.lg.Snapshot()
		if _, err := a.Append(c.batch); !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v want %v", c.name, err, c.want)
		}
		if a.LEO() != leo || len(a.lg.Index()) != n || !slices.Equal(a.lg.Snapshot(), snap) {
			t.Fatalf("%s: rejected batch changed state", c.name)
		}
		if _, err := a.Append([]int64{4}); err != nil {
			t.Fatalf("%s: unusable after rejection", c.name)
		}
	}
}

func TestErrorsDistinct(t *testing.T) {
	a, _ := New(0, 1)
	a.Append([]int64{1})
	_, eB := New(-1, 5)
	_, eM := New(0, 0)
	_, eT := a.Append([]int64{-3})
	_, eC := a.Append([]int64{2})
	got := []error{eB, eM, eT, eC}
	want := []error{ErrBadBase, ErrBadMaxMsgs, ErrNegativeTS, ErrCapacity}
	for i := range want {
		for j := range want {
			if (i == j) != errors.Is(got[i], want[j]) {
				t.Fatalf("case %d vs %d: got %v", i, j, got[i])
			}
		}
	}
}

func TestConcurrentStable(t *testing.T) {
	a, _ := New(0, 100000)
	var wg sync.WaitGroup
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			first, seen := int64(0), false
			for i := 0; i < 20000; i++ {
				if off, ok := a.Lookup(50); ok {
					if seen && off != first {
						t.Error("same t returned different offsets")
						return
					}
					first, seen = off, true
				}
			}
		}()
	}
	for i := 0; i < 300; i++ {
		a.Append([]int64{int64(i % 100)})
	}
	wg.Wait()
}

func TestSelfCheck(t *testing.T) {
	if a, err := New(100, 1000); err != nil || !a.SelfCheck() {
		t.Fatal("SelfCheck failed")
	}
}
