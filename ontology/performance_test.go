package ontology

import "testing"

// 性能不变量的含义（见设计说明）：
// “祖先序列核对”指判断“某跳的目标是否位于当前游走祖先序列中”这一
// 环判定原语。它必须是 O(1) 的哈希查询，其成本不得随“图中对象总数、
// 链接总数、权限标签种类总数”线性增长（例如不得退化为对全图所有对象
// 或全部标签做线性扫描）。
//
// 本测试以可复现的方式验证：
//  1. 每次核对都是对祖先哈希集合的 O(1) 命中判断（由代码结构保证，
//     计数器只在该 map 查询处递增）。
//  2. 把与本次遍历“完全无关”的对象、链接、标签种类放大 64 倍，
//     固定遍历的可达范围后，核对次数保持不变——证明核对不触碰、
//     不依赖全图总量。
func buildPerfGraph(unrelated int, allLabels []string) *GraphStore {
	g := NewGraphStore()
	// 与遍历相关的小固定子图（调用方只持标签 A）：
	// s ->a ->b，b 有一条 A 自环（可见环，核对次数确定）。
	for _, o := range []string{"s", "a", "b"} {
		g.AddObject(o)
	}
	_ = g.AddLink(Link{ID: "r1", From: "s", To: "a", Label: "A"})
	_ = g.AddLink(Link{ID: "r2", From: "a", To: "b", Label: "A"})
	_ = g.AddLink(Link{ID: "r3", From: "b", To: "b", Label: "A"})

	// 与遍历完全无关的巨大区域：独立对象、互不相连，标签种类众多。
	for i := 0; i < unrelated; i++ {
		id := "u" + itoa(i)
		g.AddObject(id)
		_ = g.AddLink(Link{
			ID: "ul" + itoa(i), From: id, To: id,
			Label: allLabels[i%len(allLabels)],
		})
	}
	return g
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestAncestorChecksIndependentOfTotalSize(t *testing.T) {
	sizes := []int{16, 128, 1024, 8192}
	// 标签种类随规模增长，证明核对次数也不依赖标签种类总数。
	var prev int = -1
	for round, unrelated := range sizes {
		labelKinds := 2 + unrelated/16
		allLabels := make([]string, labelKinds)
		for i := range allLabels {
			allLabels[i] = "T" + itoa(i)
		}
		g := buildPerfGraph(unrelated, allLabels)
		svc := NewService(g, nil)
		resp, err := svc.Traverse(TraverseRequest{
			Start: "b", Labels: map[string]struct{}{"A": {}}, MaxDepth: 3,
		})
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		t.Logf("unrelated=%d totalObjects=%d totalLinks=%d labelKinds=%d checks=%d",
			unrelated, 3+unrelated, 3+unrelated, labelKinds, resp.AncestorChecks)
		if prev == -1 {
			prev = resp.AncestorChecks
		} else if resp.AncestorChecks != prev {
			t.Fatalf("祖先核对次数随全图对象/链接/标签种类总量变化：%d -> %d",
				prev, resp.AncestorChecks)
		}
	}
}
