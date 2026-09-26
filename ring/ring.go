// Package ring 维护有序虚节点环，提供顺时针后继（含回绕）与节点挂入/摘除。
// 依赖 hashk。并发安全由调用方（api 包）保证。
package ring

import (
	"sort"

	"ontology/hashk"
)

// VNode 是环上一个虚节点：位置 + 所属节点 ID。
type VNode struct {
	Pos  uint32
	Node uint32
}

// Ring 是按位置升序排列的虚节点环。
type Ring struct {
	vnodes int             // 每个节点的虚节点个数 v
	sorted []VNode         // 按 Pos 升序
	nodes  map[uint32]bool // 已挂入的节点
	cmp    int             // 非导出：最近一次 Successor 检查（比较）的虚节点个数
}

// New 创建环，vnodes 为每节点虚节点个数（必须 >= 1）。
func New(vnodes int) *Ring {
	return &Ring{vnodes: vnodes, nodes: make(map[uint32]bool)}
}

// Has 报告节点是否已挂入。
func (r *Ring) Has(id uint32) bool { return r.nodes[id] }

// NodeCount 返回已挂入节点数。
func (r *Ring) NodeCount() int { return len(r.nodes) }

// AddNode 挂入节点的全部 v 个虚节点；节点已存在时不改状态、返回 false。
func (r *Ring) AddNode(id uint32) bool {
	if r.nodes[id] {
		return false
	}
	for i := 0; i < r.vnodes; i++ {
		r.sorted = append(r.sorted, VNode{Pos: hashk.VNodePos(id, uint32(i)), Node: id})
	}
	sort.Slice(r.sorted, func(a, b int) bool { return r.sorted[a].Pos < r.sorted[b].Pos })
	r.nodes[id] = true
	return true
}

// RemoveNode 摘除节点的全部虚节点；节点不存在时不改状态、返回 false。
func (r *Ring) RemoveNode(id uint32) bool {
	if !r.nodes[id] {
		return false
	}
	kept := r.sorted[:0]
	for _, vn := range r.sorted {
		if vn.Node != id {
			kept = append(kept, vn)
		}
	}
	r.sorted = kept
	delete(r.nodes, id)
	return true
}

// Successor 返回 keyPos 的顺时针后继（首个位置 >= keyPos 的虚节点，
// 全无则回绕到位置最小者）。环空时返回 ok=false。比较次数记入 cmp。
func (r *Ring) Successor(keyPos uint32) (uint32, bool) {
	if len(r.sorted) == 0 {
		return 0, false
	}
	r.cmp = 0
	idx := sort.Search(len(r.sorted), func(i int) bool {
		r.cmp++
		return r.sorted[i].Pos >= keyPos
	})
	if idx == len(r.sorted) {
		idx = 0 // 回绕
	}
	return r.sorted[idx].Node, true
}

// Snapshot 返回当前有序虚节点列表的副本（供自检/演示做朴素参照）。
func (r *Ring) Snapshot() []VNode {
	out := make([]VNode, len(r.sorted))
	copy(out, r.sorted)
	return out
}
