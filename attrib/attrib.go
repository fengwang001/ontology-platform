// Package attrib 在调用树快照上做热点归因：一遍扫描聚合，一次排序输出。
package attrib

import (
	"sort"

	"ontology/tree"
)

// Entry 是一个函数名（帧名）的归因结果。
type Entry struct {
	Frame string
	Self  int64
	Total int64 // 函数级 total：仅累加最外层同名节点的 total
}

// aggregate 一遍 DFS 统计函数级 self 与 total。
// exclude 非空时该帧被隐藏：其 self 记到最近的可见祖先，total 不计。
// total 只统计「路径上无同名祖先」的最外层节点，避免递归重复计数。
func aggregate(root *tree.Node, exclude string) (self, total map[string]int64) {
	self = map[string]int64{}
	total = map[string]int64{}
	var walk func(n *tree.Node, charge string, active map[string]bool)
	walk = func(n *tree.Node, charge string, active map[string]bool) {
		frame, visible := n.Frame, n.Frame != exclude
		if visible {
			charge = frame
		}
		if n.Self > 0 && charge != "" {
			self[charge] += n.Self
		}
		if visible && frame != "" && !active[frame] {
			total[frame] += n.Total
			active = cloneActive(active)
			active[frame] = true
		}
		for _, c := range n.Children {
			walk(c, charge, active)
		}
	}
	walk(root, "", map[string]bool{})
	return self, total
}

func cloneActive(m map[string]bool) map[string]bool {
	c := make(map[string]bool, len(m)+1)
	for k, v := range m {
		c[k] = v
	}
	return c
}

func entries(self, total map[string]int64) []Entry {
	frames := map[string]bool{}
	for f := range self {
		frames[f] = true
	}
	for f := range total {
		frames[f] = true
	}
	out := make([]Entry, 0, len(frames))
	for f := range frames {
		out = append(out, Entry{Frame: f, Self: self[f], Total: total[f]})
	}
	return out
}

// BySelf 按 self 降序归因（并列按帧名升序）。零样本返回空切片。
func BySelf(t *tree.Tree) []Entry {
	self, total := aggregate(t.Snapshot(), "")
	es := entries(self, total)
	sort.Slice(es, func(i, j int) bool {
		if es[i].Self != es[j].Self {
			return es[i].Self > es[j].Self
		}
		return es[i].Frame < es[j].Frame
	})
	return es
}

// ByTotal 按函数级 total 降序归因（并列按帧名升序）。
func ByTotal(t *tree.Tree) []Entry {
	self, total := aggregate(t.Snapshot(), "")
	es := entries(self, total)
	sort.Slice(es, func(i, j int) bool {
		if es[i].Total != es[j].Total {
			return es[i].Total > es[j].Total
		}
		return es[i].Frame < es[j].Frame
	})
	return es
}

// Excluding 返回隐藏指定帧后的归因（按 self 降序）：该帧的 self 记到最近可见祖先。
func Excluding(t *tree.Tree, frame string) []Entry {
	self, total := aggregate(t.Snapshot(), frame)
	es := entries(self, total)
	sort.Slice(es, func(i, j int) bool {
		if es[i].Self != es[j].Self {
			return es[i].Self > es[j].Self
		}
		return es[i].Frame < es[j].Frame
	})
	return es
}

// FunctionTotals 返回每个函数名的函数级 total（最外层规则）。
func FunctionTotals(t *tree.Tree) map[string]int64 {
	_, total := aggregate(t.Snapshot(), "")
	return total
}
