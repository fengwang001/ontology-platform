package api

import (
	"errors"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
)

// interval 生成一对随机端点 s < w。
func interval(r *rand.Rand) (int64, int64) {
	s, w := r.Int64N(60), r.Int64N(60)
	if s > w {
		s, w = w, s
	}
	return s, w + 1
}

// TestViewMatchesNaive 不变量1：随机序列下 View 与朴素重放逐段相同。
func TestViewMatchesNaive(t *testing.T) {
	for _, seed := range []uint64{11, 222, 3333, 44444, 555555} {
		r, e := rand.New(rand.NewPCG(seed, 0)), New(64)
		var naive []Interval
		for step := 0; step < 200; step++ {
			s, w := interval(r)
			op := r.Int64N(2)
			_, err := run(e, op, s, w)
			if op == 0 {
				if err == nil {
					naive = naiveAdd(naive, s, w)
				} else if !errors.Is(err, ErrTooMany) {
					t.Fatalf("seed %d step %d: %v", seed, step, err)
				}
			} else if nw := naiveWithdraw(naive, s, w); (err == nil) != (nw != nil) {
				t.Fatalf("seed %d step %d: withdraw 判定与朴素参照不一致", seed, step)
			} else if nw != nil {
				naive = nw
			}
			if !slices.Equal(e.View(), naive) {
				t.Fatalf("seed %d step %d: view %v != naive %v", seed, step, e.View(), naive)
			}
		}
	}
}

// TestChangelogSelfConsistent 不变量2：下游按前缀应用日志，每条 - 精确命中。
func TestChangelogSelfConsistent(t *testing.T) {
	for _, maxSeg := range []int{4, 8, 16} {
		r, e := rand.New(rand.NewPCG(uint64(maxSeg), 0)), New(maxSeg)
		var down []Interval
		for step := 0; step < 100; step++ {
			s, w := interval(r)
			log, err := run(e, r.Int64N(2), s, w)
			if err != nil {
				continue
			}
			err = applyLog(&down, log)
			down = slices.SortedFunc(slices.Values(down), byS)
			if err != nil || !slices.Equal(down, e.View()) {
				t.Fatalf("maxSeg %d step %d: 下游发散 err=%v", maxSeg, step, err)
			}
		}
	}
}

// TestMaximalInvariant 不变量3：任意操作后相邻段满足 next.S > prev.E。
func TestMaximalInvariant(t *testing.T) {
	r, e := rand.New(rand.NewPCG(7, 0)), New(32)
	for step := 0; step < 300; step++ {
		s, w := interval(r)
		run(e, r.Int64N(2), s, w)
		if v := e.View(); !maximal(v) {
			t.Fatalf("step %d: 非最大归并 %v", step, v)
		}
	}
}

// TestRejectLeavesState 不变量4：三类可判定错误互不相同，被拒后状态不变且可继续用。
func TestRejectLeavesState(t *testing.T) {
	if errors.Is(ErrInvalid, ErrTooMany) || errors.Is(ErrTooMany, ErrNotFound) || errors.Is(ErrInvalid, ErrNotFound) {
		t.Fatal("哨兵错误不互不相同")
	}
	cases := []struct {
		name     string
		fill     int
		op, s, w int64
		want     error
	}{
		{"invalid-add", 1, 0, 5, 5, ErrInvalid},
		{"invalid-withdraw", 1, 1, 9, 3, ErrInvalid},
		{"too-many", 2, 0, 40, 50, ErrTooMany},
		{"not-found", 1, 1, 100, 200, ErrNotFound},
		{"edge-only", 1, 1, 10, 100, ErrNotFound}, // 只贴边也算找不到对象
	}
	for _, c := range cases {
		e := New(2)
		for k := 0; k < c.fill; k++ {
			e.Add(int64(10*k), int64(10*k+5))
		}
		before := e.View()
		_, err := run(e, c.op, c.s, c.w)
		if !errors.Is(err, c.want) || !slices.Equal(before, e.View()) {
			t.Fatalf("%s: 拒绝语义被破坏 err=%v", c.name, err)
		}
		if _, err := e.Withdraw(0, 5); err != nil {
			t.Fatalf("%s: 被拒后无法继续使用: %v", c.name, err)
		}
	}
}

// TestConcurrentView 并发只读：N 个 goroutine 拿到的分段逐段相同，无 sleep。
func TestConcurrentView(t *testing.T) {
	e := New(16)
	for k := 0; k < 10; k++ {
		e.Add(int64(10*k), int64(10*k+5))
	}
	want, bad := e.View(), make(chan []Interval, 64)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if v := e.View(); !slices.Equal(v, want) {
					bad <- v
				}
			}
			e.SelfCheck()
		}()
	}
	wg.Wait()
	close(bad)
	for v := range bad {
		t.Fatalf("并发视图不一致: %v", v)
	}
}
