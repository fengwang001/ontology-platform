package model

// Choice 描述一次实例启动对分叉出边的选择。
// XorSplit：恰 1 个出边编号；OrSplit：非空的出边编号子集；其余节点不得出现。
type Choice map[int][]int

// ValidateChoice 校验选择表：缺失、越界、重复、多余、空集均为 ErrChoice。
func (g *Graph) ValidateChoice(ch Choice) error {
	seen := make(map[int]bool, g.N+1)
	for v, eds := range ch {
		if v < 1 || v > g.N || seen[v] {
			return ErrChoice
		}
		seen[v] = true
		switch g.Kinds[v] {
		case XorSplit:
			if len(eds) != 1 || eds[0] < 0 || eds[0] >= len(g.Out()[v]) {
				return ErrChoice
			}
		case OrSplit:
			if len(eds) == 0 {
				return ErrChoice
			}
			picked := map[int]bool{}
			for _, e := range eds {
				if e < 0 || e >= len(g.Out()[v]) || picked[e] {
					return ErrChoice
				}
				picked[e] = true
			}
		default:
			return ErrChoice
		}
	}
	for v := 1; v <= g.N; v++ {
		if (g.Kinds[v] == XorSplit || g.Kinds[v] == OrSplit) && !seen[v] {
			return ErrChoice
		}
	}
	return nil
}

// EffectiveOut 按选择裁掉未选中的分叉出边，返回有效图邻接（保持加入顺序）。
func (g *Graph) EffectiveOut(ch Choice) [][]int {
	out := g.Out()
	eff := make([][]int, g.N+1)
	for v := 1; v <= g.N; v++ {
		switch g.Kinds[v] {
		case XorSplit:
			eff[v] = []int{out[v][ch[v][0]]}
		case OrSplit:
			keep := map[int]bool{}
			for _, e := range ch[v] {
				keep[e] = true
			}
			for i, w := range out[v] {
				if keep[i] {
					eff[v] = append(eff[v], w)
				}
			}
		default:
			eff[v] = append(eff[v], out[v]...)
		}
	}
	return eff
}

// CanReach 在给定邻接上计算传递闭包：CanReach[u] 为 u 可到达的节点位图（含 u 自身）。
// 采用迭代到不动点的写法，不依赖节点编号与拓扑顺序一致。
func CanReach(n int, out [][]int) []uint64 {
	reach := make([]uint64, n+1)
	for v := 1; v <= n; v++ {
		reach[v] = 1 << uint(v)
	}
	changed := true
	for changed {
		changed = false
		for u := 1; u <= n; u++ {
			var acc uint64
			for _, w := range out[u] {
				acc |= 1<<uint(w) | reach[w]
			}
			nu := reach[u] | acc
			if nu != reach[u] {
				reach[u] = nu
				changed = true
			}
		}
	}
	return reach
}
