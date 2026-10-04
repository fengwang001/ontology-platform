// Package route 解析自根网关到目标节点的下行路径。
package route

import "ontology/topo"

var (
	ErrNotFound = topo.ErrNotFound
	ErrOffline  = topo.ErrOffline
	ErrInvalid  = topo.ErrInvalid
)

// Hop 为下行路径上的一跳：节点名与其当前纪元。
type Hop struct {
	Name  string
	Epoch int64
}

// Resolver 基于拓扑与会话状态解析下行路径。
type Resolver struct {
	graph *topo.Graph

	// ep 返回在线节点的当前纪元；不在线时 ok=false。
	// 实现不得自行加 Graph 锁（本包在读锁内调用它）。
	ep      func(name string) (epoch int64, ok bool)
	touched int
}

// NewResolver 创建路径解析器。
func NewResolver(graph *topo.Graph, epochOf func(string) (int64, bool)) *Resolver {
	return &Resolver{graph: graph, ep: epochOf}
}

// Route 返回自根网关到 node 的每一跳（名字, 当前纪元）。
// 层级约束保证路径至多 3 跳，因此触碰节点数不超过 3。
func (r *Resolver) Route(node string) ([]Hop, error) {
	if !topo.ValidName(node) {
		return nil, ErrInvalid
	}
	r.graph.RLock()
	defer r.graph.RUnlock()

	if !r.graph.HasNode(node) {
		return nil, ErrNotFound
	}
	r.touched = 0

	// 沿 parent 链收集（node, parent, 根…），再反转为根→node。
	// 每跳只查询一次纪元，故触碰计数=路径长度≤3。
	chain := make([]string, 0, 3)
	epochs := make([]int64, 0, 3)
	for cur := node; ; {
		r.touched++
		ep, ok := r.ep(cur)
		if !ok {
			return nil, ErrOffline
		}
		chain = append(chain, cur)
		epochs = append(epochs, ep)
		parent, bound := r.graph.ParentOf(cur)
		if !bound {
			// 路径末端为未绑定的根网关。
			break
		}
		cur = parent
	}

	hops := make([]Hop, 0, len(chain))
	for i := len(chain) - 1; i >= 0; i-- {
		hops = append(hops, Hop{Name: chain[i], Epoch: epochs[i]})
	}
	return hops, nil
}
