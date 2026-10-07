package fib

import "sort"

// reconstruct.go 由增量维护的 DP 状态重建数据面。
//
// 数据面不在每次更新时物化(那会随表项总数增长),而是在读取时
// 自顶向下回溯 DP 决策生成:在节点 v、继承色为 c 时,
//   - 若 c ∈ A_v,则 f_v(c) = D_v-1 由"不放置条目"达到,不放置;
//   - 否则若 base_v(c) <= K_v(base 为不放置时两孩子槽的开销和,
//     K 为放置最优条目的开销),不放置;
//   - 否则在 v 放置一个颜色为 c* 的条目,c* 取使孩子槽总开销最小
//     的颜色(候选集 Cand_v 中按固定全序取定,保证结果确定)。
// 空洞孩子槽(无路由子树)在继承色与其颜色不同时,需在槽根放置
// 一个该颜色的条目。每条放置决策恰好对应 DP 记账中的一个条目,
// 因此列出的条目数恒等于 Count。

// decide 计算节点 v 在继承色 inherited 下的最优决策:
// 是否在 v 放置条目,以及放置的颜色(不放置时返回值未定义)。
func decide(v *node, inherited color) (place bool, cstar color) {
	if _, ok := v.cand[inherited]; ok {
		return false, color{}
	}
	sumD := 0
	base := 0
	savings := make(map[color]int, 4)
	for s := 0; s < 2; s++ {
		if c := v.child[s]; c != nil {
			sumD += c.d
			base += c.d
			if _, ok := c.cand[inherited]; ok {
				base--
			}
			for col := range c.cand {
				savings[col]++
			}
		} else {
			sumD++
			if inherited != v.gamma {
				base++
			}
			savings[v.gamma]++
		}
	}
	m := 0
	for _, sv := range savings {
		if sv > m {
			m = sv
		}
	}
	k := 1 + sumD - m
	if base <= k {
		return false, color{}
	}
	// 在候选色中确定性地选择:优先本节点颜色,否则按固定全序取最小。
	if savings[v.gamma] == m {
		return true, v.gamma
	}
	var best color
	first := true
	for col, sv := range savings {
		if sv != m {
			continue
		}
		if first || colorLess(col, best) {
			best, first = col, false
		}
	}
	return true, best
}

// list 重建并返回全部数据面条目,按起始地址升序、同起始地址按
// 前缀长度升序排列。
func (t *trie) list() []Entry {
	if t.root == nil {
		return nil
	}
	var out []Entry
	var walk func(v *node, pfx uint32, depth int, inherited color)
	walk = func(v *node, pfx uint32, depth int, inherited color) {
		cur := inherited
		if place, cstar := decide(v, inherited); place {
			out = append(out, Entry{
				Prefix: Prefix{Addr: pfx, Len: depth},
				Result: cstar.result(),
			})
			cur = cstar
		}
		if depth == 32 {
			return
		}
		for s := 0; s < 2; s++ {
			childPfx := pfx | (uint32(s) << (31 - depth))
			if c := v.child[s]; c != nil {
				walk(c, childPfx, depth+1, cur)
			} else if cur != v.gamma {
				// 空洞槽:放置一个本节点颜色的条目。
				out = append(out, Entry{
					Prefix: Prefix{Addr: childPfx, Len: depth + 1},
					Result: v.gamma.result(),
				})
			}
		}
	}
	walk(t.root, 0, 0, noRouteColor)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Prefix.Addr != out[j].Prefix.Addr {
			return out[i].Prefix.Addr < out[j].Prefix.Addr
		}
		return out[i].Prefix.Len < out[j].Prefix.Len
	})
	return out
}

// dataQuery 在数据面上查询 addr 的结果。与 list 使用同一套决策
// 逻辑,只沿 addr 所在路径下推,开销为 O(地址位数)。
func (t *trie) dataQuery(addr uint32) color {
	v := t.root
	if v == nil {
		return noRouteColor
	}
	cur := noRouteColor
	d := 0
	for {
		if place, cstar := decide(v, cur); place {
			cur = cstar
		}
		if d == 32 {
			return cur
		}
		c := v.child[(addr>>(31-d))&1]
		if c == nil {
			// 落入空洞槽:若放置了槽根条目则命中该颜色。
			if cur != v.gamma {
				return v.gamma
			}
			return cur
		}
		v = c
		d++
	}
}
