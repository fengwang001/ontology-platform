package anomaly

import (
	"fmt"
	"sort"
)

type cycleFinding struct {
	category Category
	seq      []int
	edges    []EdgeType
}

func analyze(h History) Result {
	v, err := validate(h)
	if err != nil {
		ih := err.(*invalidHistory)
		return Result{Rejected: true, ErrCode: ih.code, Err: ih}
	}

	g, g1a, g1b := buildGraph(h, v)

	var reason string
	switch {
	case g1a != nil:
		reason = fmt.Sprintf("committed transaction T%d read version (T%d,%d) written by an aborted transaction on key %q",
			g1a.reader, g1a.ver.Txn, g1a.ver.Seq, g1a.key)
		return Result{Category: G1a, Level: "PL-1", Reason: reason}
	case g1b != nil:
		reason = fmt.Sprintf("committed transaction T%d read non-last-write version (T%d,%d) on key %q",
			g1b.reader, g1b.ver.Txn, g1b.ver.Seq, g1b.key)
		return Result{Category: G1b, Level: "PL-1", Reason: reason}
	}

	finding := findAnomaly(g)
	if finding == nil {
		return Result{Category: None, Level: "PL-3", Reason: "no dependency cycle among committed transactions"}
	}

	level := map[Category]string{
		G0:      "无",
		G1c:     "PL-1",
		GSingle: "PL-2",
		G2:      "PL-2+",
	}[finding.category]

	edges := make([]Edge, len(finding.seq))
	for i := range finding.seq {
		to := finding.seq[(i+1)%len(finding.seq)]
		edges[i] = Edge{From: finding.seq[i], To: to, Type: finding.edges[i]}
	}
	return Result{
		Category: finding.category,
		Level:    level,
		Witness:  append([]int(nil), finding.seq...),
		Edges:    edges,
		Reason:   describe(finding),
	}
}

func describe(f *cycleFinding) string {
	name := map[EdgeType]string{WW: "ww", WR: "wr", RW: "rw"}
	s := fmt.Sprintf("witness cycle %v with edges [", f.seq)
	for i, t := range f.edges {
		if i > 0 {
			s += ","
		}
		s += name[t]
	}
	return s + "]"
}

// classify 对一个节点序列环判定其最早命中类别。
// 同一对事务可能同时存在多种边（位掩码）：若某步仅能选 RW，则该步强制 RW；
// 其余步优先选择 WW/WR 以命中更早类别。
func classify(edges []EdgeType) Category {
	allWW := true
	rw := 0
	for _, t := range edges {
		if t&WW == 0 {
			allWW = false
		}
		if t&(WW|WR) == 0 {
			rw++ // 只能走 RW
		}
	}
	switch {
	case allWW:
		return G0
	case rw == 0:
		return G1c
	case rw == 1:
		return GSingle
	default:
		return G2
	}
}

// chooseTypes 在分类确定后为每步选定具体边类型（同序优先级 WW > WR > RW）。
func chooseTypes(edges []EdgeType, cat Category) []EdgeType {
	out := make([]EdgeType, len(edges))
	for i, t := range edges {
		switch {
		case t&WW != 0:
			out[i] = WW
		case t&WR != 0:
			out[i] = WR
		default:
			out[i] = RW
		}
	}
	if cat == G1c {
		// G1c 要求至少一条写读边：把首个可选 WR（但不是纯 WW 步）的位置定为 WR。
		for i, t := range edges {
			if t != WW && t&WR != 0 {
				out[i] = WR
				break
			}
		}
	}
	return out
}

func findAnomaly(g *graph) *cycleFinding {
	n := len(g.nodes)
	adj := make([][]int, n)
	idx := make(map[int]int, n)
	for i, id := range g.nodes {
		idx[id] = i
	}
	et := make([][]EdgeType, n)
	for i := range adj {
		et[i] = make([]EdgeType, n)
	}
	for e, mask := range g.edges {
		a, b := idx[e[0]], idx[e[1]]
		adj[a] = append(adj[a], b)
		et[a][b] = mask
	}
	for i := range adj {
		sort.Ints(adj[i])
	}

	best := map[Category]*cycleFinding{}

	// Johnson 算法枚举全部简单环；每轮只在含最小节点的强连通分量内搜索。
	for s := 0; s < n; s++ {
		sub := sccWithMin(adj, s, n)
		if sub == nil {
			continue
		}
		blocked := make([]bool, n)
		blockedB := make([]map[int]bool, n)
		for i := range blockedB {
			blockedB[i] = map[int]bool{}
		}
		stack := []int{}

		var unblock func(u int)
		unblock = func(u int) {
			blocked[u] = false
			for w := range blockedB[u] {
				delete(blockedB[u], w)
				if blocked[w] {
					unblock(w)
				}
			}
		}

		var circuit func(v int) bool
		circuit = func(v int) bool {
			if b := best[G0]; b != nil && len(stack)+1 > len(b.seq) {
				// 继续只会得到比已知见证更长的环，剪枝。
				return false
			}
			found := false
			stack = append(stack, v)
			blocked[v] = true
			for _, w := range adj[v] {
				if !sub[w] {
					continue
				}
				if w == s {
					recordCycle(stack, g.nodes, et, best)
					found = true
				} else if !blocked[w] {
					if circuit(w) {
						found = true
					}
				}
			}
			if found {
				unblock(v)
			} else {
				for _, w := range adj[v] {
					if sub[w] {
						blockedB[w][v] = true
					}
				}
			}
			stack = stack[:len(stack)-1]
			return found
		}
		circuit(s)
	}

	for _, c := range []Category{G0, G1c, GSingle, G2} {
		if best[c] != nil {
			return best[c]
		}
	}
	return nil
}

func recordCycle(stack []int, nodes []int, et [][]EdgeType, best map[Category]*cycleFinding) {
	types := make([]EdgeType, len(stack))
	for i := range stack {
		types[i] = et[stack[i]][stack[(i+1)%len(stack)]]
	}
	cat := classify(types)
	chosen := chooseTypes(types, cat)

	// 规范化：以环内最小编号事务起头（Johnson 已保证简单环，且方向固定）。
	minPos := 0
	for i := 1; i < len(stack); i++ {
		if nodes[stack[i]] < nodes[stack[minPos]] {
			minPos = i
		}
	}
	seq := make([]int, len(stack))
	rotTypes := make([]EdgeType, len(stack))
	for i := range stack {
		j := (minPos + i) % len(stack)
		seq[i] = nodes[stack[j]]
		rotTypes[i] = chosen[j]
	}

	cand := &cycleFinding{category: cat, seq: seq, edges: rotTypes}
	cur := best[cat]
	if cur == nil || lessCycle(cand, cur) {
		best[cat] = cand
	}
}

// lessCycle：先比长度（短者优先），再从事务编号序列字典序比较。
func lessCycle(a, b *cycleFinding) bool {
	if len(a.seq) != len(b.seq) {
		return len(a.seq) < len(b.seq)
	}
	for i := range a.seq {
		if a.seq[i] != b.seq[i] {
			return a.seq[i] < b.seq[i]
		}
	}
	return false
}

// sccWithMin 在只含下标 >= s 的节点诱导子图中，返回包含 s 的 SCC 成员掩码；
// 若该 SCC 只有 s 自身（本判定不产生自环边，故无环）则返回 nil。
func sccWithMin(adj [][]int, s, n int) []bool {
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = -1
	}
	var stack []int
	next := 0
	var sccOf []int

	var dfs func(v int)
	dfs = func(v int) {
		index[v] = next
		low[v] = next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range adj[v] {
			if w < s {
				continue
			}
			if index[w] == -1 {
				dfs(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] == index[v] {
			var comp []int
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			for _, w := range comp {
				if w == s {
					sccOf = comp
				}
			}
		}
	}
	dfs(s)

	if len(sccOf) <= 1 {
		return nil // 自环不产生边，单节点 SCC 无环
	}
	mask := make([]bool, n)
	for _, w := range sccOf {
		mask[w] = true
	}
	return mask
}
