package wm

import (
	"fmt"
	"sync"
	"testing"

	"ontology/drift"
)

// TestObserveReadsOneHistory：先观测 m 条，再观测第 m+1 条，
// 为判定读取的历史水位线个数必须恒为 1（O(1)，不随 m 增长）。
// 直接在同包内读取非导出字段 lastReadCount——不经任何导出接口。
func TestObserveReadsOneHistory(t *testing.T) {
	cases := []struct {
		m int
	}{{100}, {1000}, {10000}}
	for _, tc := range cases {
		m := NewManager(10, 3)
		for i := 0; i < tc.m; i++ {
			m.Observe("s", int64(i))
		}
		m.Observe("s", int64(tc.m)) // 第 m+1 条
		if m.lastReadCount != 1 {
			t.Fatalf("m=%d: history reads = %d, want 1", tc.m, m.lastReadCount)
		}
	}
}

// seq 是每个并发源喂的确定序列；阈值 10/3 下：
// Normal,Normal,Drift,Reorder,Reorder,Rollback,Normal,Drift。
var seq = []int64{0, 5, 20, 18, 17, 16, 30, 41}

const (
	perSourceDrift    = 2
	perSourceReorder  = 2
	perSourceRollback = 1
	perSourceLast     = 41
)

// TestConcurrentObserve：N 个 goroutine 各观测一个不同源的确定序列；
// 期间一个并发读者持续断言三类计数单调不减；结束后逐源核对 last 与总计数。
// 不使用 sleep 制造时序。
func TestConcurrentObserve(t *testing.T) {
	cases := []struct{ n int }{{4}, {16}, {64}}
	for _, tc := range cases {
		n := tc.n
		m := NewManager(10, 3)

		var workers sync.WaitGroup
		done := make(chan struct{})

		// 并发读者：worker 全部结束前持续取计数快照，断言单调不减。
		readerDone := make(chan struct{})
		go func() {
			defer close(readerDone)
			var pd, pr, prb int64
			for {
				select {
				case <-done:
					return
				default:
					d, r, rb := m.Counts()
					if d < pd || r < pr || rb < prb {
						t.Errorf("n=%d: counts decreased between reads: %d/%d/%d -> %d/%d/%d",
							n, pd, pr, prb, d, r, rb)
						return
					}
					pd, pr, prb = d, r, rb
				}
			}
		}()

		workers.Add(n)
		for g := 0; g < n; g++ {
			source := fmt.Sprintf("src-%d", g)
			go func() {
				defer workers.Done()
				for _, w := range seq {
					m.Observe(source, w)
				}
			}()
		}
		workers.Wait()
		close(done)
		<-readerDone

		for g := 0; g < n; g++ {
			last, ok := m.Last(fmt.Sprintf("src-%d", g))
			if !ok || last != perSourceLast {
				t.Fatalf("n=%d src-%d: last = %d (ok=%v), want %d", n, g, last, ok, perSourceLast)
			}
		}
		d, r, rb := m.Counts()
		if d != int64(n*perSourceDrift) || r != int64(n*perSourceReorder) || rb != int64(n*perSourceRollback) {
			t.Fatalf("n=%d: counts = %d/%d/%d, want %d/%d/%d",
				n, d, r, rb, n*perSourceDrift, n*perSourceReorder, n*perSourceRollback)
		}
	}
}

// TestRoutingAndCounts：多源互不干扰，分类与计数按源正确路由。
func TestRoutingAndCounts(t *testing.T) {
	m := NewManager(10, 3)
	steps := []struct {
		source string
		w      int64
		want   drift.Class
	}{
		{"a", 100, drift.Normal},
		{"b", 100, drift.Normal}, // 不同源各自的首条
		{"a", 120, drift.Drift},
		{"b", 105, drift.Normal},
		{"a", 119, drift.Reorder},
		{"a", 100, drift.Rollback},
		{"b", 105, drift.Normal}, // 等于 last：Normal，last 不变
	}
	for i, st := range steps {
		if got := m.Observe(st.source, st.w); got != st.want {
			t.Fatalf("step %d (%s,%d): got %s, want %s", i, st.source, st.w, got, st.want)
		}
	}
	if last, _ := m.Last("a"); last != 120 {
		t.Fatalf("a last = %d, want 120", last)
	}
	if last, _ := m.Last("b"); last != 105 {
		t.Fatalf("b last = %d, want 105", last)
	}
	if d, r, rb := m.Counts(); d != 1 || r != 1 || rb != 1 {
		t.Fatalf("counts = %d/%d/%d, want 1/1/1", d, r, rb)
	}
}
