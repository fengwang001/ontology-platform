package model

import (
	"errors"
	"testing"
)

func k(ns ...NodeType) []NodeType {
	return append([]NodeType{0}, ns...)
}

func TestValidateStructureAndCycle(t *testing.T) {
	// ErrStructure：n 越界。
	if err := (&Graph{N: 65, Kinds: make([]NodeType, 66)}).Validate(); !errors.Is(err, ErrStructure) {
		t.Fatalf("n=65: %v", err)
	}
	// ErrStructure：自环。
	g := &Graph{N: 3, Kinds: k(Start, Task, End), Edges: []Edge{{1, 2}, {2, 2}}}
	if err := g.Validate(); !errors.Is(err, ErrStructure) {
		t.Fatalf("self loop: %v", err)
	}
	// ErrStructure：重复边。
	g = &Graph{N: 3, Kinds: k(Start, Task, End), Edges: []Edge{{1, 2}, {1, 2}, {2, 3}}}
	if err := g.Validate(); !errors.Is(err, ErrStructure) {
		t.Fatalf("dup edge: %v", err)
	}
	// ErrStructure：Start 数量不为 1。
	g2 := &Graph{N: 3, Kinds: k(Start, Start, End), Edges: []Edge{{1, 3}, {2, 3}}}
	if err := g2.Validate(); !errors.Is(err, ErrStructure) {
		t.Fatalf("two starts: %v", err)
	}
	// ErrStructure：度不符合类型要求（Task 无出边）。
	g3 := &Graph{N: 3, Kinds: k(Start, Task, End), Edges: []Edge{{1, 2}, {2, 3}}}
	g3.Kinds[2] = End
	g3.Kinds[3] = Task
	if err := g3.Validate(); !errors.Is(err, ErrStructure) {
		t.Fatalf("bad degrees: %v", err)
	}
	// ErrStructure：边端点越界。
	g4 := &Graph{N: 3, Kinds: k(Start, Task, End), Edges: []Edge{{1, 2}, {2, 9}}}
	if err := g4.Validate(); !errors.Is(err, ErrStructure) {
		t.Fatalf("endpoint out of range: %v", err)
	}
	// ErrCycle：结构合法但 3<->4 成环。
	cyc := &Graph{
		N:     5,
		Kinds: k(Start, AndSplit, Task, Task, End),
		Edges: []Edge{{1, 2}, {2, 3}, {3, 4}, {4, 3}, {2, 5}},
	}
	if err := cyc.Validate(); !errors.Is(err, ErrCycle) {
		t.Fatalf("cycle: %v", err)
	}
	// 合法图通过。
	ok := &Graph{N: 3, Kinds: k(Start, Task, End), Edges: []Edge{{1, 2}, {2, 3}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid rejected: %v", err)
	}
}

// ErrReach 在题面严格结构约束下无法由 Validate 独立触发：与 Start 不连通
// 的分量必有入度 0 源点（只能是 Start，而全图恰一个），无环图又必止于 End。
// 因此它是按规范保留的防御性校验；这里直接白盒验证其判定逻辑。
func TestReachGuardLogic(t *testing.T) {
	out := [][]int{{}, {2}, {3}, {}}
	in := [][]int{{}, {}, {1}, {2}}
	if err := checkReach(3, k(Start, Task, End), out, in); err != nil {
		t.Fatalf("reachable flagged: %v", err)
	}
	if err := checkReach(4, k(Start, Task, End, Task),
		[][]int{{}, {2}, {3}, {}}, [][]int{{}, {}, {1}, {2}}); !errors.Is(err, ErrReach) {
		t.Fatalf("isolated node 4 must be ErrReach, got %v", err)
	}
	// 闭包不依赖编号拓扑顺序。
	reach := CanReach(3, [][]int{{}, {3}, {3}, {}})
	if reach[1]&(1<<3) == 0 {
		t.Fatalf("closure missing 1->3: %b", reach[1])
	}
}
