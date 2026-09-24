package api

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/uck"
)

// TestNineStepScenario 第三节 9 步：快照、两通道状态、恢复后 RunAll 的最终值。
func TestNineStepScenario(t *testing.T) {
	a := New(16)
	steps := []struct{ k, ch, v int }{ // 0=Arrive 1=Step 2=Barrier
		{0, 2, 5}, {1, 2, 0}, {0, 2, 2}, {0, 1, 4}, {2, 1, 1},
		{1, 2, 0}, {0, 2, 6}, {0, 1, 7}, {2, 2, 1},
	}
	do := []func(ch, v int) error{a.Arrive, func(ch, _ int) error { return a.Step(ch) }, a.Barrier}
	for _, s := range steps {
		if err := do[s.k](s.ch, s.v); err != nil {
			t.Fatalf("步 %+v: %v", s, err)
		}
	}
	snap, cs1, cs2, n := a.Snapshot()
	if n != 1 || snap.Sum != [3]int{0, 0, 5} || snap.Has[1] || !snap.Has[2] || snap.Last[2] != 5 ||
		!slices.Equal(cs1, []int{4}) || !slices.Equal(cs2, []int{2, 6}) {
		t.Fatalf("快照或通道状态错误: %+v %v %v", snap, cs1, cs2)
	}
	a.Restore()
	a.RunAll()
	if st := a.State(); st.Sum != [3]int{0, 11, 13} || st.Last != [3]int{0, 7, 6} {
		t.Fatalf("恢复后 RunAll 错误: %+v", st)
	}
}

// TestAlignedConsistency 不变量1：快照 Sum + 通道状态之和 = 屏障前到达之和。
func TestAlignedConsistency(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 291, 2026} {
		if inv1, _, _ := driveRandom(seed); !inv1 {
			t.Fatalf("seed=%d: 不变量1 不成立", seed)
		}
	}
}

// TestExactlyOnce 不变量2：屏障前每条记录恰好在「快照前已处理」与「通道状态」之一。
func TestExactlyOnce(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 291, 2026} {
		if _, inv2, _ := driveRandom(seed); !inv2 {
			t.Fatalf("seed=%d: 不变量2 不成立", seed)
		}
	}
}

// TestRestoreEquivalence 不变量3：Restore+RunAll 与直接 RunAll 结果相同。
func TestRestoreEquivalence(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 291, 2026} {
		if _, _, inv3 := driveRandom(seed); !inv3 {
			t.Fatalf("seed=%d: 不变量3 不成立", seed)
		}
	}
}

// TestRejectNoMutation 不变量4：四类错误可判定、互不相同，被拒后状态不变且可继续用。
func TestRejectNoMutation(t *testing.T) {
	a := New(2)
	a.Barrier(1, 1) // 检查点 1 开始，通道 1 已收屏障
	a.Arrive(2, 1)
	a.Arrive(2, 2) // CS2 达到上限
	before := a.State()
	cases := []struct {
		name string
		do   func() error
		want error
	}{
		{"通道号非法Arrive", func() error { return a.Arrive(0, 1) }, uck.ErrChannel},
		{"通道号非法Step", func() error { return a.Step(3) }, uck.ErrChannel},
		{"处理空队列", func() error { return a.Step(1) }, uck.ErrEmpty},
		{"同通道重复屏障", func() error { return a.Barrier(1, 1) }, uck.ErrBarrier},
		{"未完成时更大编号", func() error { return a.Barrier(2, 2) }, uck.ErrBarrier},
		{"通道状态超限", func() error { return a.Arrive(2, 3) }, uck.ErrStateLimit},
		{"首个屏障编号非1", func() error { return New(2).Barrier(1, 3) }, uck.ErrBarrier},
	}
	for _, c := range cases {
		if err := c.do(); !errors.Is(err, c.want) {
			t.Errorf("%s: 错误 %v，期望 %v", c.name, err, c.want)
		}
		if a.State() != before {
			t.Errorf("%s: 被拒后状态改变", c.name)
		}
	}
	if err := a.Barrier(2, 1); err != nil { // 被拒后仍可继续正常使用
		t.Fatalf("被拒后无法继续: %v", err)
	}
}

// TestConcurrent 并发到达/屏障/处理后，不变量 1、2 成立且恢复等价。
func TestConcurrent(t *testing.T) {
	a := New(1 << 30)
	const N = 1000
	var wg sync.WaitGroup
	for c := 1; c <= 2; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			for i := 0; i < N; i++ {
				if err := a.Arrive(c, i+1); err != nil {
					t.Error(err)
				}
				if i == N/2 {
					if err := a.Barrier(c, 1); err != nil {
						t.Error(err)
					}
				}
			}
		}(c)
	}
	for c := 1; c <= 2; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			for i := 0; i < 4*N; i++ {
				a.Step(c)
			}
		}(c)
	}
	wg.Wait()
	snap, cs1, cs2, n := a.Snapshot()
	want := (N/2 + 1) * (N/2 + 2) / 2 // 单到达 goroutine：屏障前恰到达 1..N/2+1
	if n != 1 || snap.Sum[1]+sumOf(cs1) != want || snap.Sum[2]+sumOf(cs2) != want {
		t.Fatalf("不变量1 不成立: n=%d snap=%v", n, snap.Sum)
	}
	for _, cs := range [][]int{cs1, cs2} { // 不变量2：CS 元素都是屏障前记录且不重复
		seen := map[int]bool{}
		for _, v := range cs {
			if v > N/2+1 || seen[v] {
				t.Fatalf("不变量2 不成立: CS=%v", cs)
			}
			seen[v] = true
		}
	}
	a.RunAll()
	direct := a.State()
	a.Restore()
	a.RunAll()
	if a.State() != direct {
		t.Fatal("恢复等价不成立")
	}
}
