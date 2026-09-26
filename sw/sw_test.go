package sw

import "testing"

// chainW 构造 m 个节点、单位权的链 0-1-...-(m-1) 的权重表。
func chainW(m int) ([]map[int]int64, []bool) {
	w := make([]map[int]int64, m)
	alive := make([]bool, m)
	for i := range w {
		w[i] = map[int]int64{}
		alive[i] = true
	}
	for i := 0; i+1 < m; i++ {
		w[i][i+1], w[i+1][i] = 1, 1
	}
	return w, alive
}

// TestChainHeapCheckedBounded 证明 MAS 用堆取最大：链图上每一步选节点
// 检查的候选个数 ≤2，不随 m 线性增长（全表扫描会是 O(m)）。
func TestChainHeapCheckedBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		w, alive := chainW(m)
		mas := newMAS(w, alive, 0)
		steps := 0
		for {
			if _, ok := mas.next(); !ok {
				break
			}
			steps++
			if mas.checked > 2 {
				t.Fatalf("m=%d step=%d: checked %d candidates, want <=2", m, steps, mas.checked)
			}
		}
		if steps != m-1 {
			t.Fatalf("m=%d: MAS added %d nodes, want %d", m, steps, m-1)
		}
	}
}

// TestMergeSumsWeights 钉住不变量 3：合并 (s,t) 后共同邻居边权为两者之和。
func TestMergeSumsWeights(t *testing.T) {
	w := []map[int]int64{
		{1: 3, 2: 5},
		{0: 3, 2: 2},
		{0: 5, 1: 2, 3: 1},
		{2: 1},
	}
	merge(w, 1, 3)                // s=1, t=3，共同邻居是 2
	if got := w[1][2]; got != 3 { // w(1,2)=2 + w(3,2)=1
		t.Fatalf("merged weight (1,2) = %d, want 3", got)
	}
	if got := w[2][1]; got != 3 {
		t.Fatalf("merged weight (2,1) = %d, want 3", got)
	}
	if _, ok := w[1][3]; ok {
		t.Fatal("s-t edge must be removed")
	}
	if w[3] != nil {
		t.Fatal("t's adjacency must be dropped")
	}
	if got := w[0][1]; got != 3 {
		t.Fatalf("untouched edge (0,1) = %d, want 3", got)
	}
}
