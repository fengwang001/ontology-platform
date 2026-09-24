package api

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/seg"
	"ontology/union"
)

// genOps 用 LCG 生成 n 个合法操作：坐标与到达顺序伪随机，撤回必命中。
func genOps(seed uint64, n int) []op {
	st := seed
	rnd := func(m int64) int64 { st = st*6364136223846793005 + 1442695040888963407; return int64(st>>33) % m }
	var ops []op
	for len(ops) < n {
		s := rnd(200)
		e := s + 1 + rnd(20)
		if rnd(2) == 0 {
			ops = append(ops, op{true, s, e})
		} else if slices.ContainsFunc(naiveReplay(ops), func(v Interval) bool { return seg.Overlaps(v, s, e) }) {
			ops = append(ops, op{false, s, e})
		}
	}
	return ops
}

func applyOp(t *testing.T, a *API, o op) []Change {
	t.Helper()
	var ch []Change
	var err error
	if o.add {
		ch, err = a.Add(o.s, o.e)
	} else {
		ch, err = a.Withdraw(o.s, o.e)
	}
	if err != nil {
		t.Fatalf("op %+v: %v", o, err)
	}
	return ch
}

func TestReplayMatchesNaive(t *testing.T) {
	for _, tc := range []struct {
		seed uint64
		n    int
	}{{1, 50}, {7, 200}, {99, 500}} {
		ops, a := genOps(tc.seed, tc.n), New(1<<20)
		for i, o := range ops {
			applyOp(t, a, o)
			if !slices.Equal(a.View(), naiveReplay(ops[:i+1])) {
				t.Fatalf("seed=%d op %d: %v != %v", tc.seed, i, a.View(), naiveReplay(ops[:i+1]))
			}
		}
	}
}

func TestChangelogSelfConsistent(t *testing.T) {
	for _, seed := range []uint64{3, 5} {
		ops, a, shadow := genOps(seed, 200), New(1<<20), []Interval(nil)
		for i, o := range ops {
			for _, c := range applyOp(t, a, o) {
				var ok bool
				if shadow, ok = union.ApplyChange(shadow, c); !ok {
					t.Fatalf("seed=%d op %d: '-' 未精确命中: %+v", seed, i, c)
				}
			}
			if !slices.Equal(shadow, a.View()) {
				t.Fatalf("seed=%d op %d: 影子并集与 View 不一致", seed, i)
			}
		}
	}
}

func TestViewMaximal(t *testing.T) {
	a := New(1 << 20)
	for i, o := range genOps(11, 300) {
		applyOp(t, a, o)
		if v := a.View(); !seg.Maximal(v) {
			t.Fatalf("op %d: 归并不完备: %v", i, v)
		}
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	cases := []struct {
		name  string
		setup [][2]int64
		max   int
		o     op
		want  error
	}{
		{"add s>e", [][2]int64{{0, 5}}, 4, op{true, 9, 2}, ErrInvalidInterval},
		{"add s==e", [][2]int64{{0, 5}}, 4, op{true, 3, 3}, ErrInvalidInterval},
		{"withdraw s==e", [][2]int64{{0, 5}}, 4, op{false, 7, 7}, ErrInvalidInterval},
		{"too many", [][2]int64{{0, 5}, {10, 15}}, 2, op{true, 20, 25}, ErrTooManyIntervals},
		{"not found 贴边", [][2]int64{{0, 5}}, 4, op{false, 5, 9}, ErrNotFound},
		{"not found 空并集", nil, 4, op{false, 0, 9}, ErrNotFound},
	}
	for _, tc := range cases {
		a := New(tc.max)
		for _, ad := range tc.setup {
			a.Add(ad[0], ad[1])
		}
		before := a.View()
		var err error
		if tc.o.add {
			_, err = a.Add(tc.o.s, tc.o.e)
		} else {
			_, err = a.Withdraw(tc.o.s, tc.o.e)
		}
		if !errors.Is(err, tc.want) || !slices.Equal(a.View(), before) {
			t.Errorf("%s: 判定=%v 或被拒后状态被改", tc.name, err)
		}
		probe := Interval{S: 100, E: 101} // 被拒后仍可用：并入已含段（无-op）或空并集新增
		if len(before) > 0 {
			probe = before[0]
		}
		if _, err2 := a.Add(probe.S, probe.E); err2 != nil {
			t.Errorf("%s: 被拒后不可用=%v", tc.name, err2)
		}
	}
	if errors.Is(ErrInvalidInterval, ErrTooManyIntervals) ||
		errors.Is(ErrTooManyIntervals, ErrNotFound) || errors.Is(ErrInvalidInterval, ErrNotFound) {
		t.Error("三类哨兵错误不互不相同")
	}
}

func TestConcurrentViewsConsistent(t *testing.T) {
	a := New(1 << 20)
	for _, o := range genOps(13, 100) {
		applyOp(t, a, o)
	}
	want := a.View()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if !slices.Equal(a.View(), want) || a.SelfCheck() != nil {
					t.Error("并发只读结果不一致或 SelfCheck 失败")
				}
			}
		}()
	}
	wg.Wait()
}
