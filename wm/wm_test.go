package wm

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/drift"
)

// 第三节：八步推导表逐步核对（last、分类、三计数）。
func TestEightSteps(t *testing.T) {
	mgr, _ := New(10, 3)
	ws := []int64{100, 105, 120, 118, 117, 116, 130, 141}
	wantCls := []drift.Class{drift.Normal, drift.Normal, drift.Drift, drift.Reorder,
		drift.Reorder, drift.Rollback, drift.Normal, drift.Drift}
	wantLast := []int64{100, 105, 120, 120, 120, 120, 130, 141}
	wantCnt := [][3]int64{{0, 0, 0}, {0, 0, 0}, {1, 0, 0}, {1, 1, 0},
		{1, 2, 0}, {1, 2, 1}, {1, 2, 1}, {2, 2, 1}}
	for i, w := range ws {
		got, err := mgr.Observe("s", w)
		if err != nil {
			t.Fatal(err)
		}
		last, _ := mgr.Last("s")
		d, r, rb := mgr.Counts()
		c := wantCnt[i]
		if got != wantCls[i] || last != wantLast[i] || d != c[0] || r != c[1] || rb != c[2] {
			t.Fatalf("step %d: got (%v, last=%d, cnt=%d,%d,%d), want (%v, %d, %v)",
				i, got, last, d, r, rb, wantCls[i], wantLast[i], c)
		}
	}
}

// 第四节：判定只读当前 last。先喂 m 条再喂第 m+1 条，
// 读取历史个数恒为 1，不随 m 增长（O(1)，不做整段历史重扫）。
// 同包测试直接读非导出字段 lastReads，不经任何导出接口。
func TestConstantReadCount(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		mgr, err := New(10, 3)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < m; i++ {
			if _, err := mgr.Observe("s", int64(i)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := mgr.Observe("s", int64(m)); err != nil {
			t.Fatal(err)
		}
		if mgr.lastReads != 1 {
			t.Fatalf("m=%d: lastReads=%d, want 1", m, mgr.lastReads)
		}
	}
}

// 首条水位线无历史可读：lastReads 为 0。
func TestFirstObserveReadsZero(t *testing.T) {
	mgr, _ := New(10, 3)
	if _, err := mgr.Observe("s", 7); err != nil {
		t.Fatal(err)
	}
	if mgr.lastReads != 0 {
		t.Fatalf("first observe: lastReads=%d, want 0", mgr.lastReads)
	}
}

// 并发：N 个 goroutine 各喂一个不同的源（确定序列 0,100,99,90，
// 各产 1 次 Drift/Reorder/Rollback），结束后每源 last=100、三计数各为 N，
// 期间并发读 Counts 总和单调不减。不用 sleep 制造时序。
func TestConcurrentObserve(t *testing.T) {
	mgr, _ := New(10, 3)
	const n = 16
	var bad, stop atomic.Bool
	go func() { // 读侧：计数总和单调不减
		for prev := int64(-1); !stop.Load(); {
			if x, y, z := mgr.Counts(); x+y+z < prev {
				bad.Store(true)
			} else {
				prev = x + y + z
			}
		}
	}()
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for _, w := range []int64{0, 100, 99, 90} {
				mgr.Observe(fmt.Sprintf("g%d", g), w)
			}
		}(g)
	}
	wg.Wait()
	stop.Store(true)
	for g := 0; g < n; g++ {
		if last, _ := mgr.Last(fmt.Sprintf("g%d", g)); last != 100 {
			t.Fatalf("g%d last != 100", g)
		}
	}
	if dn, rn, rb := mgr.Counts(); bad.Load() || dn != n || rn != n || rb != n {
		t.Fatalf("counts=%d,%d,%d bad=%v", dn, rn, rb, bad.Load())
	}
}
