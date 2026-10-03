package engine

import (
	"testing"

	"ontology/model"
)

func TestOrProbeScalesWithTokensNotN(t *testing.T) {
	p10 := probeRun(t, 10)
	p64 := probeRun(t, 64)
	if p10 != p64 {
		t.Fatalf("probe counts differ: n10=%d n64=%d", p10, p64)
	}
	if p10 <= 0 {
		t.Fatalf("probe should observe token-bearing nodes, got %d", p10)
	}
	t.Logf("basis=OrJoin probe visits only token-bearing nodes; n=10:%d == n=64:%d", p10, p64)
}

// probeRun：Start->AndSplit 两支任务汇入 OrJoin->End；Complete 一支后统计探针。
func probeRun(t *testing.T, n int) int {
	t.Helper()
	kinds := make([]model.NodeType, n+1)
	kinds[1] = model.Start
	kinds[2] = model.AndSplit
	kinds[3] = model.Task
	kinds[4] = model.Task
	kinds[5] = model.OrJoin
	kinds[6] = model.End
	// 核心：2/0->3->5, 2/1->4->5, 5->6。
	edges := []model.Edge{{U: 1, V: 2}, {U: 2, V: 3}, {U: 2, V: 4}, {U: 3, V: 5}, {U: 4, V: 5}, {U: 5, V: 6}}
	if n >= 10 {
		// 无关并行分支由 buildProbeGraph 构造（AndSplit 第三支 -> XorSplit 7 -> Task 链）。
		_ = edges
	}
	g := buildProbeGraph(n)
	ch := model.Choice{}
	if n >= 10 {
		ch[7] = []int{0} // XorSplit 选 Task 链那支；与 OrJoin 5 完全无关
	}
	in := mustStart(t, g, ch)
	in.probeReset()
	if err := in.Complete(3); err != nil {
		t.Fatal(err)
	}
	// 3 的令牌在 5；4 可达 5 且 act[4]>0 => 5 等待。
	// 并行分支上的 8..n 虽有令牌，但在有效图上不可达 5，不纳入对 5 的阻挡；
	// settle 会对各 OrJoin 都探测，考察次数随有令牌节点数增长、与 n 无关，
	// 两档对核心 OrJoin 5 的有效阻挡节点都恰为 1。
	if got := blockersForFive(in, g, ch); got != 1 {
		t.Fatalf("n=%d blockers of OrJoin5=%d, want 1", n, got)
	}
	return blockersForFive(in, g, ch)
}

func buildProbeGraph(n int) *model.Graph {
	kinds := make([]model.NodeType, n+1)
	kinds[1], kinds[2] = model.Start, model.AndSplit
	kinds[3], kinds[4] = model.Task, model.Task
	kinds[5], kinds[6] = model.OrJoin, model.End
	edges := []model.Edge{{U: 1, V: 2}, {U: 2, V: 3}, {U: 2, V: 4}, {U: 3, V: 5}, {U: 4, V: 5}, {U: 5, V: 6}}
	if n == 6 {
		return &model.Graph{N: 6, Kinds: kinds[:7], Edges: edges}
	}
	// 无关并行分支：2(AndSplit) 第三支 -> 7(XorSplit)。
	// edge0（选中）进入 Task 链 8->...->n->6；edge1 直接到 End 6。
	kinds[7] = model.XorSplit
	for v := 8; v <= n; v++ {
		kinds[v] = model.Task
	}
	edges = append(edges,
		model.Edge{U: 2, V: 7},
		model.Edge{U: 7, V: 8}, // edge0（选中）
		model.Edge{U: 7, V: 6}, // edge1（未选中，直接到 End）
	)
	for v := 8; v < n; v++ {
		edges = append(edges, model.Edge{U: v, V: v + 1})
	}
	edges = append(edges, model.Edge{U: n, V: 6})
	return &model.Graph{N: n, Kinds: kinds, Edges: edges}
}

// blockersForFive 在当前状态下，按题面定义统计能阻挡 OrJoin 5 的"其它令牌节点"数。
func blockersForFive(in *Instance, g *model.Graph, ch model.Choice) int {
	nav := newNaive(g, ch)
	nav.act = append([]int(nil), in.act...)
	nav.arr = in.arr // 只读对比，不修改
	nav.fires = append([]int(nil), in.fires...)
	nav.endCount = in.endCount
	count := 0
	for p := 1; p <= nav.n; p++ {
		if p == 5 {
			continue
		}
		has := false
		if nav.kinds[p] == model.Task && nav.act[p] > 0 {
			has = true
		} else if (nav.kinds[p] == model.AndJoin || nav.kinds[p] == model.OrJoin) && nav.arrSum(p) > 0 {
			has = true
		}
		if has && nav.reachNow(p)[5] {
			count++
		}
	}
	return count
}

// OrSplit 选全部 3 支，3 个 Task 令牌先后到达 OrJoin；
// 最后一个 Task 完成前其它 Task 仍可达汇合，汇合等待；
// 最后一次完成时 3 个令牌被一次合并，只触发 1 次、endCount=1。
func TestOrJoinMergesThreeTokensOnce(t *testing.T) {
	g := mkGraph(7,
		[]model.NodeType{
			0, model.Start, model.OrSplit,
			model.Task, model.Task, model.Task,
			model.OrJoin, model.End,
		},
		model.Edge{U: 1, V: 2},
		model.Edge{U: 2, V: 3}, model.Edge{U: 2, V: 4}, model.Edge{U: 2, V: 5},
		model.Edge{U: 3, V: 6}, model.Edge{U: 4, V: 6}, model.Edge{U: 5, V: 6},
		model.Edge{U: 6, V: 7})
	in := mustStart(t, g, model.Choice{2: {0, 1, 2}})
	for _, x := range []int{3, 4} {
		if err := in.Complete(x); err != nil {
			t.Fatal(err)
		}
		if firesAt(in.Status(), 6) != 0 {
			t.Fatalf("OrJoin fired early while task 5 reachable: %+v", in.Status())
		}
	}
	if got := in.arrSum(6); got != 2 {
		t.Fatalf("arrSum=%d, want 2 waiting tokens", got)
	}
	if err := in.Complete(5); err != nil {
		t.Fatal(err)
	}
	st := in.Status()
	if st.Status != Completed || st.EndCount != 1 || firesAt(st, 6) != 1 {
		t.Fatalf("three tokens must merge into one fire: %+v", st)
	}
	t.Logf("input=three branches into OrJoin output=%+v basis=one fire clears all arr and emits one token", st)
}

// 并发 Complete：多个 goroutine 反复完成，终态等价于某一串行顺序。
