package ring

import (
	"testing"

	"ontology/hashk"
)

// TestGetComparisonCountSublinear 证明 Successor 用有序结构 + 二分定位：
// 环上 m 个虚节点时，一次 Get 检查（比较）的虚节点个数 ≤ ceil(log2 m)+1，
// 不随 m 线性增长。计数器 cmp 是非导出字段，仅同包测试可读。
func TestGetComparisonCountSublinear(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		r := New(1)
		for id := uint32(0); id < uint32(m); id++ {
			if !r.AddNode(id*2654435 + 1) {
				t.Fatalf("m=%d: AddNode(%d) rejected", m, id)
			}
		}
		if got := len(r.Snapshot()); got != m {
			t.Fatalf("m=%d: vnode count = %d", m, got)
		}
		for _, key := range []uint32{0, 42, 1 << 31, 1<<32 - 1} {
			if _, ok := r.Successor(hashk.KeyPos(key)); !ok {
				t.Fatalf("m=%d key=%d: successor missing", m, key)
			}
			limit := 1
			for x := 1; x < m; x <<= 1 {
				limit++
			}
			if r.cmp > limit {
				t.Fatalf("m=%d key=%d: compared %d vnodes, limit %d (linear scan?)", m, key, r.cmp, limit)
			}
			if r.cmp >= m && m > 16 {
				t.Fatalf("m=%d key=%d: compared %d vnodes, grows with m", m, key, r.cmp)
			}
		}
	}
}

// TestSuccessorEdgeCases 钉住回绕与 >= 边界（等值命中自身）。
func TestSuccessorEdgeCases(t *testing.T) {
	r := New(2)
	for _, id := range []uint32{1, 2, 3} {
		r.AddNode(id)
	}
	// 回绕：H(50)=0xe6d5c492 大于所有虚节点位置，须回绕到 0x0d2adbb1（节点2）。
	if n, ok := r.Successor(hashk.KeyPos(50)); !ok || n != 2 {
		t.Fatalf("wrap: got %d,%v want 2,true", n, ok)
	}
	// 等值：H(256)=0x3779b100 恰为节点1虚节点，>= 语义须归节点1（> 会错归节点3）。
	if n, ok := r.Successor(hashk.KeyPos(256)); !ok || n != 1 {
		t.Fatalf("equal-pos: got %d,%v want 1,true", n, ok)
	}
	// 空环。
	if _, ok := New(1).Successor(0); ok {
		t.Fatal("empty ring: successor should report ok=false")
	}
}
