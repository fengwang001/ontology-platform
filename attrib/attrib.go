// Package attrib 在已有调用树上做热点归因：节点排序与函数级（递归安全）归因。
package attrib

import (
	"sort"

	"ontology/tree"
)

// NodeStat 是一个调用路径节点的归因行。
type NodeStat struct {
	Name      string
	Self      int64
	Total     int64
	Truncated bool
}

// FuncStat 是函数级归因行（递归帧合并后的结果）。
type FuncStat struct {
	Name  string
	Self  int64
	Total int64
}

// NodesBySelf 在不重建树的前提下一遍扫描收集节点，再按 self 降序排序。
func NodesBySelf(t *tree.Tree) []NodeStat {
	t.RLock()
	defer t.RUnlock()
	stats := collect(t.Root)
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Self != stats[j].Self {
			return stats[i].Self > stats[j].Self
		}
		return stats[i].Name < stats[j].Name
	})
	return stats
}

// NodesByTotal 一遍扫描后按 total 降序排序。
func NodesByTotal(t *tree.Tree) []NodeStat {
	t.RLock()
	defer t.RUnlock()
	stats := collect(t.Root)
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Total != stats[j].Total {
			return stats[i].Total > stats[j].Total
		}
		return stats[i].Name < stats[j].Name
	})
	return stats
}

func collect(n *tree.Node) []NodeStat {
	var out []NodeStat
	var walk func(*tree.Node)
	walk = func(n *tree.Node) {
		out = append(out, NodeStat{n.Name, n.Self, n.Total, n.Truncated})
		for _, c := range n.Children() {
			walk(c)
		}
	}
	for _, c := range n.Children() {
		walk(c)
	}
	return out
}

// Functions 做递归安全的函数级归因（一遍 DFS + 一次排序）：
// 每条根→叶路径上，函数 F 的 total 只由该路径第一个（最外层）F 节点认领，
// 内层 F 的 total 丢弃以免与祖先 F 重复计入；self 可跨所有 F 节点直接求和。
func Functions(t *tree.Tree) []FuncStat {
	t.RLock()
	defer t.RUnlock()
	type acc struct{ self, total int64 }
	byName := map[string]*acc{}
	order := []string{}

	var walk func(*tree.Node, map[string]bool)
	walk = func(n *tree.Node, seen map[string]bool) {
		outer := !seen[n.Name]
		a := byName[n.Name]
		if a == nil {
			a = &acc{}
			byName[n.Name] = a
			order = append(order, n.Name)
		}
		a.self += n.Self
		if outer {
			a.total += n.Total
		}
		childSeen := seen
		if outer {
			childSeen = cloneSeen(seen)
			childSeen[n.Name] = true
		}
		for _, c := range n.Children() {
			walk(c, childSeen)
		}
	}

	for _, c := range t.Root.Children() {
		walk(c, map[string]bool{})
	}

	out := make([]FuncStat, 0, len(order))
	for _, name := range order {
		a := byName[name]
		out = append(out, FuncStat{name, a.self, a.total})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Exclude 模拟“折叠掉指定函数帧”后的 self 归因：
// 被排除子树内停留的样本（被排除节点自身的 self）上推给最近的未排除祖先，
// 不重建树，一遍 DFS 完成；被排除节点本身不出现在结果中。
func Exclude(t *tree.Tree, excluded map[string]bool) []FuncStat {
	t.RLock()
	defer t.RUnlock()
	type acc struct{ self int64 }
	byName := map[string]*acc{}
	order := []string{}

	var walk func(*tree.Node, *tree.Node)
	walk = func(n, nearest *tree.Node) {
		target := nearest
		if !excluded[n.Name] {
			target = n
			a := byName[n.Name]
			if a == nil {
				a = &acc{}
				byName[n.Name] = a
				order = append(order, n.Name)
			}
			a.self += n.Self
		} else if nearest != nil {
			byName[nearest.Name].self += n.Self
		}
		for _, c := range n.Children() {
			walk(c, target)
		}
	}
	for _, c := range t.Root.Children() {
		walk(c, nil)
	}

	out := make([]FuncStat, 0, len(order))
	for _, name := range order {
		out = append(out, FuncStat{name, byName[name].self, 0})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Self != out[j].Self {
			return out[i].Self > out[j].Self
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func cloneSeen(m map[string]bool) map[string]bool {
	c := make(map[string]bool, len(m)+1)
	for k := range m {
		c[k] = true
	}
	return c
}
