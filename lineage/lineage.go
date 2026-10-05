// Package lineage 维护列间派生边：同一 (src,dst) 至多一条边、
// 每列入边至多 16 条，并通过可达性查询支撑无环维护。
package lineage

// MaxInDegree 是每列允许的最大入边数。
const MaxInDegree = 16

// Kind 是派生边种类，决定级别经边传递时的映射 f。
type Kind int

const (
	Copy Kind = iota // f(x) = x
	Mask             // f(x) = max(x-1, 0)
	Hash             // f(x) = max(x-2, 0)
	Agg              // f(x) = min(x, 2)
)

// ValidKind 报告 kind 是否越界。
func ValidKind(k Kind) bool { return k >= Copy && k <= Agg }

// Map 返回级别 x 经该种边传递后的值。
func (k Kind) Map(x int) int {
	switch k {
	case Mask:
		if x-1 > 0 {
			return x - 1
		}
		return 0
	case Hash:
		if x-2 > 0 {
			return x - 2
		}
		return 0
	case Agg:
		if x < 2 {
			return x
		}
		return 2
	default: // Copy
		return x
	}
}

// Graph 是派生边的出/入双索引。不是并发安全的，由上层串行化。
// 变更方法假定调用方已完成全部校验（重复、入度、成环）。
type Graph struct {
	out map[string]map[string]Kind // src -> dst -> kind
	in  map[string]map[string]Kind // dst -> src -> kind
}

// New 创建空图。
func New() *Graph {
	return &Graph{
		out: make(map[string]map[string]Kind),
		in:  make(map[string]map[string]Kind),
	}
}

// HasEdge 报告 src->dst 边是否存在。
func (g *Graph) HasEdge(src, dst string) bool {
	_, ok := g.out[src][dst]
	return ok
}

// InDegree 返回 dst 的入边数。
func (g *Graph) InDegree(dst string) int { return len(g.in[dst]) }

// AddEdge 登记 src->dst 边。
func (g *Graph) AddEdge(src, dst string, k Kind) {
	if g.out[src] == nil {
		g.out[src] = make(map[string]Kind)
	}
	if g.in[dst] == nil {
		g.in[dst] = make(map[string]Kind)
	}
	g.out[src][dst] = k
	g.in[dst][src] = k
}

// RemoveEdge 删除 src->dst 边。
func (g *Graph) RemoveEdge(src, dst string) {
	delete(g.out[src], dst)
	if len(g.out[src]) == 0 {
		delete(g.out, src)
	}
	delete(g.in[dst], src)
	if len(g.in[dst]) == 0 {
		delete(g.in, dst)
	}
}

// OutEdges 返回 src 的出边表（dst -> kind）。返回内部 map，只读使用。
func (g *Graph) OutEdges(src string) map[string]Kind { return g.out[src] }

// InEdges 返回 dst 的入边表（src -> kind）。返回内部 map，只读使用。
func (g *Graph) InEdges(dst string) map[string]Kind { return g.in[dst] }

// Reachable 报告是否存在 from 到 to 的非空有向路径（用于成环判定：
// 新增 src->dst 成环当且仅当 Reachable(dst, src)）。
func (g *Graph) Reachable(from, to string) bool {
	if from == to {
		return true
	}
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for v := range g.out[u] {
			if v == to {
				return true
			}
			if !seen[v] {
				seen[v] = true
				stack = append(stack, v)
			}
		}
	}
	return false
}
