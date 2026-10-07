package fib

// naive_test.go 是独立的对照模型:用最直接的通用递归 DP 计算最少
// 条目数(不共享生产代码的闭式合并逻辑),并用前缀表扫描实现控制面
// 查询。随机测试用它逐步比对生产实现。

import (
	"errors"
	"fmt"
	"sort"
)

// naiveModel 用 map 直接维护控制面,容量语义与 Manager 一致,
// 但回滚用整表快照、聚合用全量重算(对照模型,允许全量重聚合)。
type naiveModel struct {
	routes map[Prefix]Nexthop
	cap    int
}

func newNaiveModel(capacity int) *naiveModel {
	return &naiveModel{routes: make(map[Prefix]Nexthop), cap: capacity}
}

func (m *naiveModel) snapshot() map[Prefix]Nexthop {
	cp := make(map[Prefix]Nexthop, len(m.routes))
	for p, nh := range m.routes {
		cp[p] = nh
	}
	return cp
}

func (m *naiveModel) restore(s map[Prefix]Nexthop) { m.routes = s }

// controlQuery 扫描全部路由做最长前缀匹配。
func (m *naiveModel) controlQuery(addr uint32) Result {
	best := -1
	res := Result{Kind: NoRoute}
	for p, nh := range m.routes {
		if p.Contains(addr) && p.Len > best {
			best = p.Len
			res = nexthopColor(nh).result()
		}
	}
	return res
}

// naiveNode 是对照模型独立构建的 trie 节点。
type naiveNode struct {
	child   [2]*naiveNode
	control bool
	gamma   color
}

// optimalCount 用通用树形 DP 独立计算最少条目数:
// f(v,c) = min( g(c), 1 + min_c' g(c') ),g(c) 为两孩子槽在继承色 c
// 下的开销和,空槽视为颜色为 v.gamma 的均匀子树。
func (m *naiveModel) optimalCount() int {
	if len(m.routes) == 0 {
		return 0
	}
	// 收集颜色集合(无路由 + 黑洞 + 全部下一跳)。
	colorSet := []color{noRouteColor, {kind: BlackholeRoute}}
	seen := map[color]bool{noRouteColor: true, {kind: BlackholeRoute}: true}
	for _, nh := range m.routes {
		c := nexthopColor(nh)
		if !seen[c] {
			seen[c] = true
			colorSet = append(colorSet, c)
		}
	}
	// 构建 trie。
	root := &naiveNode{}
	for p, nh := range m.routes {
		v := root
		for d := 0; d < p.Len; d++ {
			bit := (p.Addr >> (31 - d)) & 1
			if v.child[bit] == nil {
				v.child[bit] = &naiveNode{}
			}
			v = v.child[bit]
		}
		v.control = true
		v.gamma = nexthopColor(nh)
	}
	// 自顶向下传播继承色。
	var propagate func(v *naiveNode, inherited color)
	propagate = func(v *naiveNode, inherited color) {
		if !v.control {
			v.gamma = inherited
		}
		next := v.gamma
		if v.control {
			next = v.gamma
		}
		for s := 0; s < 2; s++ {
			if v.child[s] != nil {
				propagate(v.child[s], next)
			}
		}
	}
	propagate(root, noRouteColor)
	// 通用 DP:每个节点返回按 colorSet 下标的函数值。
	nc := len(colorSet)
	var solve func(v *naiveNode) []int
	solve = func(v *naiveNode) []int {
		var l, r []int
		if v.child[0] != nil {
			l = solve(v.child[0])
		}
		if v.child[1] != nil {
			r = solve(v.child[1])
		}
		res := make([]int, nc)
		minG := 1 << 30
		for i, c := range colorSet {
			g := 0
			if l == nil {
				if c != v.gamma {
					g++
				}
			} else {
				g += l[i]
			}
			if r == nil {
				if c != v.gamma {
					g++
				}
			} else {
				g += r[i]
			}
			res[i] = g
			if g < minG {
				minG = g
			}
		}
		for i := range res {
			if 1+minG < res[i] {
				res[i] = 1 + minG
			}
		}
		return res
	}
	f := solve(root)
	for i, c := range colorSet {
		if c == noRouteColor {
			return f[i]
		}
	}
	panic("unreachable")
}

// 以下 put/delete/batch/setCapacity 复刻 Manager 的语义
// (校验、优先级、回滚),作为随机测试的行为对照。

func (m *naiveModel) put(p Prefix, nh Nexthop) error {
	if !p.Valid() || !nh.valid() {
		return ErrInvalidArgument
	}
	old, had := m.routes[p]
	if had && old == nh {
		return nil // 空操作
	}
	snap := m.snapshot()
	m.routes[p] = nh
	if m.optimalCount() > m.cap {
		m.restore(snap)
		return ErrCapacityExceeded
	}
	return nil
}

func (m *naiveModel) delete(p Prefix) error {
	if !p.Valid() {
		return ErrInvalidArgument
	}
	if _, ok := m.routes[p]; !ok {
		return ErrRouteNotFound
	}
	snap := m.snapshot()
	delete(m.routes, p)
	if m.optimalCount() > m.cap {
		m.restore(snap)
		return ErrCapacityExceeded
	}
	return nil
}

func (m *naiveModel) batch(ops ...Op) error {
	for _, op := range ops {
		if !op.Prefix.Valid() || (!op.Delete && !op.Nexthop.valid()) {
			return ErrInvalidArgument
		}
	}
	snap := m.snapshot()
	for _, op := range ops {
		if op.Delete {
			if _, ok := m.routes[op.Prefix]; !ok {
				m.restore(snap)
				return ErrRouteNotFound
			}
			delete(m.routes, op.Prefix)
		} else {
			m.routes[op.Prefix] = op.Nexthop
		}
	}
	if m.optimalCount() > m.cap {
		m.restore(snap)
		return ErrCapacityExceeded
	}
	return nil
}

func (m *naiveModel) setCapacity(c int) error {
	if c < 0 {
		return ErrInvalidArgument
	}
	if c < m.optimalCount() {
		return ErrCapacityExceeded
	}
	m.cap = c
	return nil
}

// entriesLookup 对数据面条目列表做最长前缀匹配查询。
func entriesLookup(entries []Entry, addr uint32) Result {
	best := -1
	res := Result{Kind: NoRoute}
	for _, e := range entries {
		if e.Prefix.Contains(addr) && e.Prefix.Len > best {
			best = e.Prefix.Len
			res = e.Result
		}
	}
	return res
}

// errIs 判断两个错误是否属于同一类别(都不报错也视为一致)。
func errIs(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b)
}

// sortedRoutes 返回控制面路由的稳定快照,用于拒绝后状态比对。
func (m *naiveModel) sortedRoutes() []string {
	var out []string
	for p, nh := range m.routes {
		out = append(out, prefixString(p)+"->"+nexthopString(nh))
	}
	sort.Strings(out)
	return out
}

func prefixString(p Prefix) string {
	return fmt.Sprintf("%d.%d.%d.%d/%d", byte(p.Addr>>24), byte(p.Addr>>16), byte(p.Addr>>8), byte(p.Addr), p.Len)
}

func nexthopString(n Nexthop) string {
	if n.Blackhole {
		return "blackhole"
	}
	return n.Value
}
