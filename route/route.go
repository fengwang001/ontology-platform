// Package route 解析自根网关到目标节点的下行路径。
//
// 只读：公开方法取 topo 的读锁；*Locked 原语假定调用方已持锁。
package route

import (
	"sync/atomic"

	"ontology/topo"
)

// Hop 为下行路径上的一跳：节点名与其当前纪元。
type Hop struct {
	Node  string
	Epoch topo.Epoch
}

// EpochProvider 返回在线节点当前纪元；不在线时 ok 为 false。
type EpochProvider interface {
	EpochLocked(name string) (topo.Epoch, bool)
}

// Resolver 持有拓扑与会话引用。
type Resolver struct {
	t       *topo.Topo
	ep      EpochProvider
	touched atomic.Int64
}

// New 创建路径解析器。
func New(t *topo.Topo, ep EpochProvider) *Resolver {
	return &Resolver{t: t, ep: ep}
}

// Route 返回自根网关到 node 的每一跳（含 node）。
func (r *Resolver) Route(node string) ([]Hop, error) {
	r.t.RLock()
	defer r.t.RUnlock()
	return r.RouteLocked(node)
}

// RouteLocked 为 Route 的持锁原语。
// 判定次序：ErrInvalid > ErrNotFound > ErrOffline（含任一祖先离线）。
func (r *Resolver) RouteLocked(node string) ([]Hop, error) {
	if !topo.ValidName(node) {
		return nil, topo.ErrInvalid
	}
	parent, ok := r.t.ParentLocked(node)
	if !ok {
		return nil, topo.ErrNotFound
	}
	chain := []string{node}
	cur := node
	for parent != "" {
		chain = append(chain, parent)
		cur = parent
		var exist bool
		parent, exist = r.t.ParentLocked(cur)
		if !exist {
			return nil, topo.ErrNotFound
		}
	}
	_ = cur
	hops := make([]Hop, 0, len(chain))
	for i := len(chain) - 1; i >= 0; i-- {
		ep, live := r.ep.EpochLocked(chain[i])
		if !live {
			return nil, topo.ErrOffline
		}
		hops = append(hops, Hop{Node: chain[i], Epoch: ep})
	}
	r.touched.Store(int64(len(hops)))
	return hops, nil
}

// RouteTouched 返回最近一次 Route 调用触碰的节点数（成功时为路径长度，<=3）。
func (r *Resolver) RouteTouched() int {
	r.t.RLock()
	defer r.t.RUnlock()
	return int(r.touched.Load())
}
