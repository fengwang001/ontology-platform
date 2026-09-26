package median

import (
	"math/rand/v2"
	"testing"

	"ontology/wmid"
)

// TestTraversalBound 证明 Find 沿树 O(log m) 定位：
// 最近一次 Find 遍历的节点数（非导出计数器 last）不随 m 线性增长。
func TestTraversalBound(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		tree := wmid.New()
		r := rand.New(rand.NewPCG(uint64(m), 7))
		for _, v := range r.Perm(m) {
			if err := tree.Insert(int64(v), int64(v%7+1)); err != nil {
				t.Fatalf("m=%d insert: %v", m, err)
			}
		}
		f := New(tree)
		if _, err := f.Find(); err != nil {
			t.Fatalf("m=%d find: %v", m, err)
		}
		bound := int64(2) // 2*ceil(log2(m)) + 2
		for x := m; x > 1; x = (x + 1) / 2 {
			bound += 2
		}
		if got := f.last.Load(); got > bound {
			t.Errorf("m=%d: traversed %d nodes, bound %d", m, got, bound)
		}
	}
}

// TestFindEmpty 空树查询返回 ErrEmpty。
func TestFindEmpty(t *testing.T) {
	if _, err := New(wmid.New()).Find(); err != ErrEmpty {
		t.Fatalf("want ErrEmpty, got %v", err)
	}
}
