package reachability

import (
	"fmt"
	"sync"
)

// Graph 维护一个带重数的有向图及其全量可达点对。
//
// 内部状态：
//   - mult：每条有向边当前的重数（只保留重数为正的边）；
//   - edgeOut / edgeIn：实际存在（重数为正）的边的出 / 入邻接索引；
//   - out / in：按源点 / 汇点索引的可达点对，两者互为冗余，便于增删时双向传播。
//
// 所有导出方法都可在任意 goroutine 中并发调用；读操作（查询、快照、解释）
// 之间可并发执行，写操作（加边、删边）被串行化。快照与判定在逐点对层面一致。
type Graph struct {
	mu      sync.RWMutex
	mult    map[Pair]int
	edgeOut map[string]map[string]struct{}
	edgeIn  map[string]map[string]struct{}
	out     map[string]map[string]struct{}
	in      map[string]map[string]struct{}
}

// New 返回一个空的 Graph。
func New() *Graph {
	return &Graph{
		mult:    make(map[Pair]int),
		edgeOut: make(map[string]map[string]struct{}),
		edgeIn:  make(map[string]map[string]struct{}),
		out:     make(map[string]map[string]struct{}),
		in:      make(map[string]map[string]struct{}),
	}
}

// AddEdge 将边 (from, to) 的重数加一，并把经由新边的可达点对并入闭包。
// 节点名为空或重数将超限时拒绝，且状态不发生任何变化。
func (g *Graph) AddEdge(from, to string) error {
	return g.AddEdgeN(from, to, 1)
}

// AddEdgeN 将边 (from, to) 的重数加 n。n 必须为正整数。
// 校验全部通过后才会修改状态，被拒绝的调用不改变重数、可达集合或索引。
func (g *Graph) AddEdgeN(from, to string, n int) error {
	if n <= 0 {
		return fmt.Errorf("%w: count must be a positive integer, got %d", ErrInvalidArgument, n)
	}
	if from == "" {
		return fmt.Errorf("%w: from node name", ErrEmptyNodeName)
	}
	if to == "" {
		return fmt.Errorf("%w: to node name", ErrEmptyNodeName)
	}
	p := Pair{From: from, To: to}

	g.mu.Lock()
	defer g.mu.Unlock()

	if m := g.mult[p]; m+n > MaxMultiplicity {
		return fmt.Errorf("%w: edge %q -> %q multiplicity %d + %d exceeds %d",
			ErrMultiplicityOverflow, from, to, m, n, MaxMultiplicity)
	}
	existed := g.mult[p] > 0
	g.mult[p] += n
	if !existed {
		// 仅在边从无到有时需要并入新的可达点对；重数增加不改变图结构。
		g.addEdgeLocked(p)
	}
	return nil
}

// RemoveEdge 将边 (from, to) 的重数减一。
// 重数归零时先移除所有可能受影响的可达点对，再对仍可达的点对重新推导加回。
// 边不存在（重数为 0）或节点名为空时拒绝，且状态不发生任何变化。
func (g *Graph) RemoveEdge(from, to string) error {
	if from == "" {
		return fmt.Errorf("%w: from node name", ErrEmptyNodeName)
	}
	if to == "" {
		return fmt.Errorf("%w: to node name", ErrEmptyNodeName)
	}
	p := Pair{From: from, To: to}

	g.mu.Lock()
	defer g.mu.Unlock()

	m, ok := g.mult[p]
	if !ok || m <= 0 {
		return fmt.Errorf("%w: edge %q -> %q", ErrEdgeNotFound, from, to)
	}
	if m > 1 {
		// 边仍然存在（重数为正），可达性不发生变化。
		g.mult[p] = m - 1
		return nil
	}
	g.removeEdgeLastLocked(p)
	return nil
}

// Reachable 报告 from 是否可达 to（存在长度至少为 1 的有向路径）。
func (g *Graph) Reachable(from, to string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.reachableLocked(from, to)
}

// Explain 返回 from 到 to 的可达判定依据。
// reachable 为 true 时 path 是一条具体见证路径（含端点，长度至少为 2）；
// 为 false 时 path 为 nil，reason 说明判定依据。
// 节点名为空时 reason 指出该非法输入。
func (g *Graph) Explain(from, to string) (reachable bool, path []string, reason string) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.explainLocked(from, to)
}

// Pairs 返回当前全部可达点对，按 (From, To) 字典序排序，结果确定。
func (g *Graph) Pairs() []Pair {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.pairsLocked()
}

// Edges 返回当前全部重数为正的边及其重数，按 (From, To) 字典序排序。
func (g *Graph) Edges() []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.edgesLocked()
}

// Multiplicity 返回边 (from, to) 当前的重数；边不存在时为 0。
func (g *Graph) Multiplicity(from, to string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.mult[Pair{From: from, To: to}]
}
