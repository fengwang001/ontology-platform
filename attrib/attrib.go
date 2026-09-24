// Package attrib 在调用树快照上做热点归因：按 self/total 排序的
// 函数级汇总，以及「排除某帧后」的归因。排序查询只做一遍扫描加
// 一次排序，不重建树。
package attrib

import (
	"sort"

	"ontology/tree"
)

// Hot 是一个函数（帧名）的归因结果。
type Hot struct {
	Frame string
	Self  uint64 // 所有同名节点 self 之和（self 互不相交，可直接相加）
	Total uint64 // 仅统计「祖先链上无同名帧」的最外层节点的 total
}

var rebuilds uint64 // 树重建次数；排序类查询必须保持为 0

// Rebuilds 返回归因过程中重建树的次数（仅 Exclude 会重建派生树）。
func Rebuilds() uint64 { return rebuilds }

// BySelf 按 self 降序返回函数级归因，一趟扫描加一次排序。
func BySelf(t *tree.Tree) []Hot {
	hs := aggregate(t)
	sort.Slice(hs, func(i, j int) bool {
		if hs[i].Self != hs[j].Self {
			return hs[i].Self > hs[j].Self
		}
		return hs[i].Frame < hs[j].Frame
	})
	return hs
}

// ByTotal 按 total 降序返回函数级归因，一趟扫描加一次排序。
func ByTotal(t *tree.Tree) []Hot {
	hs := aggregate(t)
	sort.Slice(hs, func(i, j int) bool {
		if hs[i].Total != hs[j].Total {
			return hs[i].Total > hs[j].Total
		}
		return hs[i].Frame < hs[j].Frame
	})
	return hs
}

// aggregate 做函数级汇总：self 全量相加；total 只加最外层同名节点，
// 避免递归帧的祖先/后代重复计入（见 DESIGN.md 第 2 节）。
func aggregate(t *tree.Tree) []Hot {
	byFrame := map[string]*Hot{}
	onPath := map[string]bool{}
	var rec func(n *tree.Node)
	rec = func(n *tree.Node) {
		if n != t.Root { // 虚拟根不参与归因
			h := byFrame[n.Frame]
			if h == nil {
				h = &Hot{Frame: n.Frame}
				byFrame[n.Frame] = h
			}
			h.Self += n.Self
			if !onPath[n.Frame] {
				h.Total += n.Total
			}
		}
		onPath[n.Frame] = true
		for _, c := range n.Children {
			rec(c)
		}
		delete(onPath, n.Frame)
	}
	rec(t.Root)
	hs := make([]Hot, 0, len(byFrame))
	for _, h := range byFrame {
		hs = append(hs, *h)
	}
	return hs
}

// Exclude 返回排除指定帧后的派生树：该帧节点被移除，其 self 丢弃，
// 子节点提升到父节点并与同名兄弟合并。只有它会重建树（计 1 次）。
func Exclude(t *tree.Tree, frame string) *tree.Tree {
	rebuilds++
	nt := tree.New()
	nt.Samples = t.Samples
	nt.TruncatedSamples = t.TruncatedSamples
	nt.Root = excludeNode(t.Root, frame)
	return nt
}

func excludeNode(n *tree.Node, frame string) *tree.Node {
	cp := &tree.Node{Frame: n.Frame, Self: n.Self, Total: n.Total, Truncated: n.Truncated}
	for _, c := range n.Children {
		if c.Frame == frame {
			for _, gc := range c.Children { // 提升被排除节点的子节点
				addMerged(cp, excludeNode(gc, frame))
			}
			continue // 被排除节点的 self/total 丢弃
		}
		addMerged(cp, excludeNode(c, frame))
	}
	return cp
}

func addMerged(parent, child *tree.Node) {
	for _, c := range parent.Children {
		if c.Frame == child.Frame {
			c.Self += child.Self
			c.Total += child.Total
			c.Truncated = c.Truncated || child.Truncated
			for _, gc := range child.Children {
				addMerged(c, gc)
			}
			return
		}
	}
	parent.Children = append(parent.Children, child)
}
