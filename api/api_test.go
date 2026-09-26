package api

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

func TestNaiveConsistency(t *testing.T) {
	for _, cap := range []int64{1, 4, 16} {
		rng := rand.New(rand.NewSource(cap * 7))
		sc, _ := New(cap)
		nf := newNaive(cap)
		var ids []int64
		for step := 0; step < 300; step++ {
			if len(ids) > 0 && rng.Intn(3) == 0 {
				i := rng.Intn(len(ids))
				if err := sc.Release(ids[i]); err != nil {
					t.Fatalf("release: %v", err)
				}
				nf.release(ids[i])
				ids = append(ids[:i], ids[i+1:]...)
				continue
			}
			a, b := rng.Int63n(30), rng.Int63n(30)
			a, b = min(a, b), max(a, b)
			need := 1 + rng.Int63n(cap)
			idS, okS, errS := sc.Reserve(a, b+1, need)
			_, okN := nf.reserve(a, b+1, need)
			if errS != nil || okS != okN {
				t.Fatalf("step %d: got(%v,%v) want %v", step, okS, errS, okN)
			}
			if okS {
				ids = append(ids, idS)
			}
			if sc.Active() != len(nf.ivs) {
				t.Fatalf("step %d: Active=%d naive=%d", step, sc.Active(), len(nf.ivs))
			}
		}
	}
}

func TestCapacityBound(t *testing.T) {
	cases := []struct {
		pre     [][3]int64
		s, e, n int64
		want    bool
	}{
		{[][3]int64{{0, 5, 4}}, 5, 9, 7, true},             // 相接不重叠
		{[][3]int64{{0, 5, 4}}, 4, 6, 7, false},            // 点 4 处 4+7>10
		{[][3]int64{{0, 5, 4}}, 0, 5, 6, true},             // 恰好装满
		{[][3]int64{{0, 5, 4}}, 0, 5, 7, false},            // 超一即拒
		{[][3]int64{{0, 9, 1}, {2, 4, 9}}, 0, 6, 1, false}, // 峰值 10 已满
	}
	for i, tc := range cases {
		sc, _ := New(10)
		for _, iv := range tc.pre {
			sc.Reserve(iv[0], iv[1], iv[2]) // 表内前置均合法
		}
		_, got, err := sc.Reserve(tc.s, tc.e, tc.n)
		if err != nil || got != tc.want {
			t.Fatalf("case %d: got (%v,%v) want %v", i, got, err, tc.want)
		}
	}
}

func TestReleaseInvalidates(t *testing.T) {
	sc, _ := New(10)
	id1, _, _ := sc.Reserve(0, 10, 10)
	_, full, _ := sc.Reserve(0, 1, 1)
	err1 := sc.Release(id1)
	_, reuse, _ := sc.Reserve(0, 10, 10)
	err2 := sc.Release(id1)
	if full || err1 != nil || !reuse || !errors.Is(err2, ErrNoID) {
		t.Fatalf("full=%v err1=%v reuse=%v err2=%v", full, err1, reuse, err2)
	}
}

func TestFailureNoTrace(t *testing.T) {
	sc, _ := New(5)
	sc.Reserve(0, 2, 1)
	bads := []struct {
		run  func() error
		want error
	}{
		{func() error { _, e := New(0); return e }, ErrCapacity},
		{func() error { _, _, e := sc.Reserve(3, 4, 0); return e }, ErrNeed},
		{func() error { _, _, e := sc.Reserve(3, 4, 6); return e }, ErrNeed},
		{func() error { _, _, e := sc.Reserve(4, 4, 1); return e }, ErrRange},
		{func() error { _, _, e := sc.Reserve(5, 4, 1); return e }, ErrRange},
		{func() error { return sc.Release(999) }, ErrNoID},
	}
	for i, b := range bads {
		if err := b.run(); !errors.Is(err, b.want) || sc.Active() != 1 {
			t.Fatalf("bad op %d: want %v, Active=%d", i, b.want, sc.Active())
		}
	}
	if _, ok, _ := sc.Reserve(2, 4, 4); !ok {
		t.Fatal("scheduler must keep working after rejected ops")
	}
}

func TestErrorsDistinct(t *testing.T) {
	seen := map[error]bool{}
	for _, e := range []error{ErrCapacity, ErrNeed, ErrRange, ErrNoID} {
		seen[e] = true
	}
	if len(seen) != 4 {
		t.Fatal("sentinel errors must be distinct")
	}
}

func TestConcurrentReserve(t *testing.T) {
	const N = 64
	sc, _ := New(1 << 40)
	var wg sync.WaitGroup
	var stop atomic.Bool
	go func() {
		for !stop.Load() {
			if a := sc.Active(); a < 0 || a > N {
				t.Errorf("Active=%d out of range", a)
				return
			}
		}
	}()
	okc := make(chan bool, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int64) {
			defer wg.Done()
			_, ok, err := sc.Reserve(i*4, i*4+2, 1)
			okc <- ok && err == nil
		}(int64(i))
	}
	wg.Wait()
	stop.Store(true)
	close(okc)
	fails := 0
	for ok := range okc {
		if !ok {
			fails++
		}
	}
	if fails > 0 || sc.Active() != N {
		t.Fatalf("fails=%d Active=%d want %d", fails, sc.Active(), N)
	}
}
