package anomaly

import "sort"

// 环类别对边的限制：
//   - G0：每条边只能是 WW；
//   - G1c：每条边只能是 WW 或 WR（不得出现 RW）；
//   - G-single：全环恰好一条 RW 边，其余为 WW/WR；
//   - G2：全环含 RW 边且不止一条（恰一条者归 G-single）。

const rwCapMany = 2 // 状态里只需区分 0 / 1 / >=2 条 RW 边

func (g *graph) sortedNeighbors(u int) []int {
	nb := make([]int, 0, len(g.adj[u]))
	for v := range g.adj[u] {
		nb = append(nb, v)
	}
	sort.Ints(nb)
	return nb
}

// shortestPlainCycleLen 求只允许 useMask 类型边的最短环长度。
// 从 s 出发且中途只经过编号 > s 的节点，保证 s 是环内最小编号。
// 用于 G0（useMask=WW）与 G1c（useMask=WW|WR）。
func (g *graph) shortestPlainCycleLen(s int, useMask EdgeMask) int {
	dist := map[int]int{s: 0}
	q := []int{s}
	best := 0
	for len(q) > 0 {
		u := q[0]
		q = q[1:]
		d := dist[u]
		if best != 0 && d+1 >= best {
			continue
		}
		for _, v := range g.sortedNeighbors(u) {
			if v < s || g.adj[u][v]&useMask == 0 {
				continue
			}
			if v == s {
				if cand := d + 1; best == 0 || cand < best {
					best = cand
				}
				continue
			}
			if _, seen := dist[v]; !seen {
				dist[v] = d + 1
				q = append(q, v)
			}
		}
	}
	return best
}

// shortestRWCycleLen 求带 RW 计数状态的最短环长度。
// exactOne=true：恰好一条 RW（G-single）；false：至少两条 RW（G2）。
func (g *graph) shortestRWCycleLen(s int, exactOne bool) int {
	type state struct{ node, rw int }
	start := state{s, 0}
	dist := map[state]int{start: 0}
	q := []state{start}
	best := 0
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		d := dist[cur]
		if best != 0 && d+1 >= best {
			continue
		}
		for _, v := range g.sortedNeighbors(cur.node) {
			if v < s {
				continue
			}
			mask := g.adj[cur.node][v]
			tryStep := func(useRW bool) {
				var add int
				if useRW {
					if mask&EdgeRW == 0 {
						return
					}
					add = 1
				} else if mask&(EdgeWW|EdgeWR) == 0 {
					return
				}
				nrw := cur.rw + add
				if exactOne && nrw > 1 {
					return
				}
				if nrw > rwCapMany {
					nrw = rwCapMany
				}
				if v == s {
					ok := (!exactOne && nrw >= 2) || (exactOne && nrw == 1)
					if ok {
						if cand := d + 1; best == 0 || cand < best {
							best = cand
						}
					}
					return
				}
				ns := state{v, nrw}
				if _, seen := dist[ns]; !seen {
					dist[ns] = d + 1
					q = append(q, ns)
				}
			}
			tryStep(false)
			tryStep(true)
		}
	}
	return best
}

type cycleSpec struct {
	exactOne bool // 仅用于非 plain 模式
	plain    bool
	useMask  EdgeMask
}

// findWitness 求某类环的规范见证环：
// 最短；等长取事务编号序列字典序最小；序列以环内最小编号起头。
// 返回 nil 表示该类环不存在。
func (g *graph) findWitness(spec cycleSpec) *Cycle {
	bestLen := 0
	bestStart := 0
	for _, s := range g.nodes {
		var l int
		if spec.plain {
			l = g.shortestPlainCycleLen(s, spec.useMask)
		} else {
			l = g.shortestRWCycleLen(s, spec.exactOne)
		}
		if l != 0 && (bestLen == 0 || l < bestLen) {
			bestLen = l
			bestStart = s
		}
	}
	if bestLen == 0 {
		return nil
	}

	// 定长 DFS：起点固定为能取到最短长度的最小编号，每一步在邻居中
	// 按编号升序尝试，第一条可行路径即等长下字典序最小的序列。
	path := []int{bestStart}
	usedEdges := []EdgeMask{}
	visited := map[int]bool{bestStart: true}

	var dfs func(u, rw int) bool
	dfs = func(u, rw int) bool {
		if len(path) == bestLen {
			use, ok := closingEdge(g.adj[u][bestStart], rw, spec)
			if ok {
				usedEdges = append(usedEdges, use)
				return true
			}
			return false
		}
		for _, v := range g.sortedNeighbors(u) {
			if v <= bestStart || visited[v] {
				continue
			}
			use, nrw, ok := stepEdge(g.adj[u][v], rw, spec)
			if !ok {
				continue
			}
			path = append(path, v)
			usedEdges = append(usedEdges, use)
			visited[v] = true
			if dfs(v, nrw) {
				return true
			}
			visited[v] = false
			usedEdges = usedEdges[:len(usedEdges)-1]
			path = path[:len(path)-1]
		}
		return false
	}
	if !dfs(bestStart, 0) {
		return nil
	}
	return &Cycle{Txns: append([]int(nil), path...), Edges: append([]EdgeMask(nil), usedEdges...)}
}

// stepEdge 为非闭合步选择实际使用的边类型；优先非 RW，再 RW。
func stepEdge(mask EdgeMask, rw int, spec cycleSpec) (EdgeMask, int, bool) {
	if spec.plain {
		if mask&spec.useMask != 0 {
			return pickPlain(mask, spec.useMask), rw, true
		}
		return 0, rw, false
	}
	if mask&(EdgeWW|EdgeWR) != 0 {
		return pickPlain(mask, EdgeWW|EdgeWR), rw, true
	}
	if mask&EdgeRW != 0 {
		nrw := rw + 1
		if spec.exactOne && nrw > 1 {
			return 0, rw, false
		}
		return EdgeRW, nrw, true
	}
	return 0, rw, false
}

func closingEdge(mask EdgeMask, rw int, spec cycleSpec) (EdgeMask, bool) {
	if spec.plain {
		if mask&spec.useMask != 0 {
			return pickPlain(mask, spec.useMask), true
		}
		return 0, false
	}
	if mask&(EdgeWW|EdgeWR) != 0 {
		ok := (!spec.exactOne && rw >= 2) || (spec.exactOne && rw == 1)
		if ok {
			return pickPlain(mask, EdgeWW|EdgeWR), true
		}
	}
	if mask&EdgeRW != 0 {
		nrw := rw + 1
		ok := (!spec.exactOne && nrw >= 2) || (spec.exactOne && nrw == 1)
		if ok {
			return EdgeRW, true
		}
	}
	return 0, false
}

// pickPlain 在允许集合内选择实际边类型：优先 WR，否则 WW。
func pickPlain(mask, allow EdgeMask) EdgeMask {
	if mask&EdgeWR != 0 && allow&EdgeWR != 0 {
		return EdgeWR
	}
	return EdgeWW
}
