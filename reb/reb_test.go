package reb

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/agg"
)

func genSrc(seed int64, n, keys int) []string {
	rng, src := rand.New(rand.NewSource(seed)), make([]string, n)
	for i := range src {
		src[i] = fmt.Sprint(rng.Intn(keys))
	}
	return src
}

func buildWithCrashes(t *testing.T, n, chunk, crashes int) *Rebuilder {
	t.Helper()
	r, _ := New(genSrc(int64(n), n, 64), chunk)
	for c := 0; c < crashes; c++ {
		_ = r.Start()
		_ = r.Step()
		_ = r.Crash()
	}
	_ = r.Start()
	for r.processed < n {
		_ = r.Step()
	}
	if _, err := r.Commit(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMatchesNaiveReplay(t *testing.T) {
	for _, tc := range []struct{ n, chunk int }{{7, 2}, {100, 3}, {1000, 10}, {5000, 17}} {
		src, rng := genSrc(int64(tc.n), tc.n, 50), rand.New(rand.NewSource(int64(tc.chunk)))
		r, _ := New(src, tc.chunk)
		ops := []func() error{r.Start, r.Step, r.Crash}
		for done := false; !done; {
			if i := rng.Intn(4); i < 3 {
				_ = ops[i]()
			} else if _, err := r.Commit(); err == nil {
				done = true
			}
		}
		if !agg.Equal(r.View(), agg.Replay(src)) {
			t.Fatalf("n=%d: view != naive replay", tc.n)
		}
	}
}

func TestOldViewReadableDuringRebuild(t *testing.T) {
	r, _ := New([]string{"a", "b", "a", "c", "b", "a"}, 2)
	old, oldGen := r.View(), r.Gen()
	check := func(where string) {
		t.Helper()
		if !agg.Equal(r.View(), old) || r.Gen() != oldGen {
			t.Fatalf("%s: old view changed", where)
		}
	}
	for _, crash := range []bool{false, true} {
		_ = r.Start()
		for step := 0; step < 3; step++ {
			_ = r.Step()
			check("building")
		}
		if crash {
			_ = r.Crash()
		}
	}
	check("after crash")
}

// 不变量3 + 复杂度：崩溃续跑视图一致，且 applied ∈ [n, n+c·chunk]。
func TestCrashResumeNoDupNoLoss(t *testing.T) {
	for _, tc := range []struct{ n, chunk, crashes int }{{6, 2, 1}, {1000, 10, 7}, {10000, 10, 13}, {10000, 10, 23}} {
		r := buildWithCrashes(t, tc.n, tc.chunk, tc.crashes)
		if !agg.Equal(r.View(), agg.Replay(genSrc(int64(tc.n), tc.n, 64))) {
			t.Fatalf("n=%d: resumed view != no-crash build", tc.n)
		}
		if r.applied < tc.n || r.applied > tc.n+tc.crashes*tc.chunk {
			t.Fatalf("n=%d c=%d: applied=%d out of [%d,%d]",
				tc.n, tc.crashes, r.applied, tc.n, tc.n+tc.crashes*tc.chunk)
		}
	}
}

func TestRejectedOpsLeaveStateUnchanged(t *testing.T) {
	r, _ := New([]string{"x", "y"}, 1)
	_, e0 := New([]string{"x"}, 0)
	e1, e3 := r.Step(), r.Crash()
	_, e2 := r.Commit()
	_ = r.Start()
	e4 := r.Start()
	_, e5 := r.Commit()
	got := []error{e0, e1, e2, e3, e4, e5}
	want := []error{ErrBadChunk, ErrNotBuilding, ErrNotBuilding, ErrNotBuilding, ErrBusy, ErrIncomplete}
	distinct := map[error]bool{}
	for i := range got {
		if got[i] != want[i] { // 哨兵错误，直接可比
			t.Fatalf("case %d: got %v want %v", i, got[i], want[i])
		}
		distinct[got[i]] = true
	}
	if len(distinct) != 4 || len(r.View()) != 0 || r.Gen() != 0 {
		t.Fatal("sentinels not distinct or state changed")
	}
	for r.processed < 2 { // 仍可正常使用
		_ = r.Step()
	}
	if g, err := r.Commit(); err != nil || g != 1 || !agg.Equal(r.View(), map[string]int{"x": 1, "y": 1}) {
		t.Fatal("unusable after rejections")
	}
}

func TestConcurrentViewConsistency(t *testing.T) {
	r := buildWithCrashes(t, 500, 7, 0)
	want := agg.Replay(genSrc(500, 500, 64))
	var stop, bad atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if !agg.Equal(r.View(), want) || r.Gen() < 1 || SelfCheck() != nil {
					bad.Store(true)
					return
				}
			}
		}()
	}
	for k := 0; k < 20 && !bad.Load(); k++ {
		_ = r.Start()
		for r.processed < 500 {
			_ = r.Step()
		}
		_, _ = r.Commit()
	}
	stop.Store(true)
	wg.Wait()
	if bad.Load() {
		t.Fatal("torn read or selfcheck failure during concurrent rebuild")
	}
}
