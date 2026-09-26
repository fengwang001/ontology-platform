package elect

import (
	"math/rand"
	"sync"
	"testing"
)

// naive 朴素重算：逐个扫描全部节点统计得票，取达到多数派的候选。
func naive(c *Cluster) int {
	_, vf := c.State()
	cnt := map[int]int{}
	for _, v := range vf {
		if v >= 0 {
			cnt[v]++
		}
	}
	for cand, n := range cnt {
		if n >= c.Size()/2+1 {
			return cand
		}
	}
	return -1
}

// TestWinnerReadsConstant 全部节点投给候选 0 后调用 Winner，
// 断言读取节点个数是不随 m 增长的小常数（增量计票，非全表扫描）。
func TestWinnerReadsConstant(t *testing.T) {
	for _, m := range []int{101, 501, 1001, 5001, 9999} {
		c := New(m)
		if err := c.StartElection(0); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		for _, k := range rand.Perm(m - 1) { // 随机到达顺序
			if ok, err := c.RequestVote(k+1, 0, 1); !ok || err != nil {
				t.Fatalf("m=%d node=%d: ok=%v err=%v", m, k+1, ok, err)
			}
		}
		if w := c.Winner(); w != 0 {
			t.Fatalf("m=%d: winner=%d, want 0", m, w)
		}
		if c.reads > 1 {
			t.Fatalf("m=%d: Winner read %d nodes, want a small constant", m, c.reads)
		}
	}
}

// TestWinnerConcurrent 多 goroutine 并发只读同一就绪实例，结果必须一致。
func TestWinnerConcurrent(t *testing.T) {
	c := New(5)
	if err := c.StartElection(2); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 1} {
		if ok, err := c.RequestVote(n, 2, 1); !ok || err != nil {
			t.Fatalf("node %d: ok=%v err=%v", n, ok, err)
		}
	}
	const g = 32
	got := make([]int, g)
	var wg sync.WaitGroup
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				got[i] = c.Winner()
			}
		}(i)
	}
	wg.Wait()
	for i, w := range got {
		if w != 2 {
			t.Fatalf("reader %d: winner=%d, want 2", i, w)
		}
	}
}

// TestWinnerMatchesNaive 随机操作序列（含越界/负任期/自投等被拒操作）后，
// Winner 必须始终等于朴素重算结果。
func TestWinnerMatchesNaive(t *testing.T) {
	for _, tc := range []struct{ n, ops int }{{3, 200}, {5, 400}, {9, 800}} {
		c := New(tc.n)
		r := rand.New(rand.NewSource(int64(tc.n*1000 + tc.ops)))
		for i := 0; i < tc.ops; i++ {
			node := r.Intn(tc.n+2) - 1 // 偶尔越界
			if r.Intn(2) == 0 {
				_ = c.StartElection(node)
			} else {
				_, _ = c.RequestVote(node, r.Intn(tc.n+2)-1, r.Intn(4)-1) // 偶尔负任期/自投
			}
			if w, nw := c.Winner(), naive(c); w != nw {
				t.Fatalf("n=%d op=%d: winner=%d, naive=%d", tc.n, i, w, nw)
			}
		}
	}
}
