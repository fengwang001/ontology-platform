// Package attrib 在树快照上做热点归因，不重建树。
package attrib

import (
	"sort"

	"ontology/tree"
)

// FuncStat 是一个函数的聚合归因。
type FuncStat struct {
	Func  string
	Self  int
	Total int
}

// Analyzer 持有一份不可变树快照；所有归因只读遍历它。
type Analyzer struct {
	root         *tree.Node
	rebuildCount int // 归因触发的树重建次数，恒为 0
}

// New 基于快照构造分析器。
func New(root *tree.Node) *Analyzer { return &Analyzer{root: root} }

// RebuildCount 返回归因过程中重建树的次数（必须为 0）。
func (a *Analyzer) RebuildCount() int { return a.rebuildCount }

type acc struct{ self, total int }

// ByFunc 一遍扫描计算函数级 self/total。
// total 只累加“不被同名函数祖先包含”的最外层节点。
func (a *Analyzer) ByFunc() []FuncStat {
	m := map[string]*acc{}
	var walk func(n *tree.Node, seen map[string]bool)
	walk = func(n *tree.Node, seen map[string]bool) {
		if n != a.root {
			st := m[n.Frame.Func]
			if st == nil {
				st = &acc{}
				m[n.Frame.Func] = st
			}
			st.self += n.Self
			if !seen[n.Frame.Func] {
				st.total += n.Total
				seen[n.Frame.Func] = true
			}
		}
		for _, c := range n.Children {
			next := cloneSeen(seen)
			if n != a.root {
				next[n.Frame.Func] = true
			}
			walk(c, next)
		}
	}
	walk(a.root, map[string]bool{})
	return sorted(m)
}

func cloneSeen(s map[string]bool) map[string]bool {
	c := make(map[string]bool, len(s)+1)
	for k := range s {
		c[k] = true
	}
	return c
}

func sorted(m map[string]*acc) []FuncStat {
	out := make([]FuncStat, 0, len(m))
	for fn, st := range m {
		out = append(out, FuncStat{Func: fn, Self: st.self, Total: st.total})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Func < out[j].Func
	})
	return out
}

func allNodes(root *tree.Node) []*tree.Node {
	var out []*tree.Node
	var walk func(n *tree.Node)
	walk = func(n *tree.Node) {
		if n != root {
			out = append(out, n)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}

func sortNodes(ns []*tree.Node, bySelf bool) []*tree.Node {
	sort.Slice(ns, func(i, j int) bool {
		v := func(n *tree.Node) int { return n.Total }
		if bySelf {
			v = func(n *tree.Node) int { return n.Self }
		}
		if v(ns[i]) != v(ns[j]) {
			return v(ns[i]) > v(ns[j])
		}
		return ns[i].Frame.Func < ns[j].Frame.Func
	})
	return ns
}

// NodesBySelf 返回按 self 降序的节点。
func (a *Analyzer) NodesBySelf() []*tree.Node {
	return sortNodes(allNodes(a.root), true)
}

// NodesByTotal 返回按 total 降序的节点。
func (a *Analyzer) NodesByTotal() []*tree.Node {
	return sortNodes(allNodes(a.root), false)
}

// Exclude 返回“排除指定函数帧后”的函数级归因：
// 命中 exclude 的节点从路径中移除，其子节点提升挂到最近未被排除的祖先下，
// total 取提升后子树的采样数；随后按函数做最外层去重聚合。
func (a *Analyzer) Exclude(exclude string) []FuncStat {
	m := map[string]*acc{}
	var walk func(n *tree.Node, seen map[string]bool)
	walk = func(n *tree.Node, seen map[string]bool) {
		if n != a.root {
			st := m[n.Frame.Func]
			if st == nil {
				st = &acc{}
				m[n.Frame.Func] = st
			}
			st.self += n.Self
			if !seen[n.Frame.Func] {
				st.total += n.Total
				seen[n.Frame.Func] = true
			}
		}
		for _, c := range n.Children {
			if c.Frame.Func == exclude {
				for _, gc := range c.Children {
					walk(gc, cloneSeen(seen))
				}
				continue
			}
			next := cloneSeen(seen)
			if n != a.root {
				next[n.Frame.Func] = true
			}
			walk(c, next)
		}
	}
	walk(a.root, map[string]bool{})
	return sorted(m)
}
