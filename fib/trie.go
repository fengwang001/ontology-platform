package fib

// trie.go 维护控制面路由的二进制 trie(深度不超过 32,只沿路由路径
// 展开),并在每个节点上增量维护用于最少条目聚合的树形 DP 状态。
//
// 模型:控制面把每个地址染上一种颜色(无路由/黑洞/某下一跳),颜色由
// 覆盖该地址的最长路由前缀决定。数据面要用最少的前缀条目(条目颜色
// 可以是无路由)复现同一着色,未被任何条目覆盖的地址视为无路由。
//
// 对每个节点 v 维护 f_v(c):在"继承色"为 c(即祖先条目决定的当前
// 颜色)时,v 子树内复现着色所需的最少条目数。可证明它恒为
//
//	f_v(c) = D_v - [c ∈ A_v]
//
// 其中 D_v 是默认开销,A_v 是"候选色集合"(使开销减一的颜色集合)。
// 设 v 的两个孩子槽(孩子不存在时视为颜色为 v.gamma 的均匀子树,
// 其函数为 (D=1, A={v.gamma}))对应的函数为 (D1,A1)、(D2,A2),令
// savings(c) = [c∈A1]+[c∈A2],m = max savings,则
//
//	m == 0:  D_v = D1+D2,     A_v = ∅
//	m >= 1:  D_v = 1+D1+D2-m, A_v = { c : savings(c) == m }
//
// 根节点在继承色为"无路由"下的函数值即全表最少条目数。
//
// 每个节点另存 gamma:控制节点的自有下一跳,或非控制节点从最近路由
// 祖先继承的颜色。均匀"空洞"子树(不含任何路由的孩子槽)的颜色就是
// 该节点的 gamma,因此 DP 合并只需要本节点的 gamma 与两个孩子的状态,
// 更新只需沿受影响路径自底向上重算,开销与无关路由总数无关。

type node struct {
	child   [2]*node
	control bool // 该节点是否挂有控制面路由
	gamma   color
	d       int                // DP 默认开销 D_v
	cand    map[color]struct{} // DP 候选色集合 A_v
}

// trie 是路由与增量 DP 状态的载体。所有修改方法都不做并发控制,
// 由 Manager 统一加锁。
type trie struct {
	root  *node
	stats *Stats
}

// recompute 由两个孩子的 DP 状态重算节点 n 的 (d, cand)。
func (t *trie) recompute(n *node) {
	t.stats.Recomputes++
	sumD := 0
	savings := make(map[color]int, 4)
	for s := 0; s < 2; s++ {
		if c := n.child[s]; c != nil {
			sumD += c.d
			for col := range c.cand {
				savings[col]++
			}
		} else {
			// 空洞槽:颜色为 n.gamma 的均匀子树,(D=1, A={gamma})。
			sumD++
			savings[n.gamma]++
		}
	}
	m := 0
	for _, sv := range savings {
		if sv > m {
			m = sv
		}
	}
	if m == 0 {
		n.d = sumD
		n.cand = nil
		return
	}
	n.d = 1 + sumD - m
	cand := make(map[color]struct{}, len(savings))
	for col, sv := range savings {
		if sv == m {
			cand[col] = struct{}{}
		}
	}
	n.cand = cand
}

// fixGamma 把子树 n 中、未被更深层控制节点隔断的非控制节点的继承色
// 更新为 g,并按后序自底向上重算这些节点。开销正比于受影响地址范围
// 内的 trie 节点数。
func (t *trie) fixGamma(n *node, g color) {
	if n == nil || n.control {
		return
	}
	t.stats.GammaUpdates++
	n.gamma = g
	t.fixGamma(n.child[0], g)
	t.fixGamma(n.child[1], g)
	t.recompute(n)
}

// applyPut 写入(或覆盖)一条路由,返回原值、原值是否存在、状态是否
// 真的发生变化(写入与原值完全相同时为不改变任何状态的空操作)。
// 调用方需保证参数合法。
func (t *trie) applyPut(p Prefix, nh color) (old color, had, changed bool) {
	if t.root == nil {
		t.root = &node{gamma: noRouteColor}
	}
	// 自根向下定位目标节点,沿途按需创建非控制节点。
	var path []*node
	v := t.root
	inherited := noRouteColor
	for d := 0; d < p.Len; d++ {
		if v.control {
			inherited = v.gamma
		}
		path = append(path, v)
		bit := (p.Addr >> (31 - d)) & 1
		if v.child[bit] == nil {
			v.child[bit] = &node{gamma: inherited}
		}
		v = v.child[bit]
	}
	if v.control && v.gamma == nh {
		return nh, true, false // 与原值完全相同:空操作
	}
	old, had = v.gamma, v.control
	v.control = true
	v.gamma = nh
	// v 的下一跳成为其下方非控制节点的新继承色。
	t.fixGamma(v.child[0], nh)
	t.fixGamma(v.child[1], nh)
	// 自底向上重算受影响路径。
	t.recompute(v)
	for i := len(path) - 1; i >= 0; i-- {
		t.recompute(path[i])
	}
	return old, had, true
}

// applyDelete 撤销一条路由,返回原值与是否存在。调用方需保证参数合法。
func (t *trie) applyDelete(p Prefix) (old color, found bool) {
	if t.root == nil {
		return color{}, false
	}
	var path []*node
	v := t.root
	inherited := noRouteColor
	for d := 0; d < p.Len; d++ {
		if v.control {
			inherited = v.gamma
		}
		path = append(path, v)
		bit := (p.Addr >> (31 - d)) & 1
		if v.child[bit] == nil {
			return color{}, false
		}
		v = v.child[bit]
	}
	if !v.control {
		return color{}, false
	}
	old = v.gamma
	v.control = false
	if v.child[0] != nil || v.child[1] != nil {
		// v 仍有子路由:降级为非控制节点,继承色改为上方最近路由的颜色,
		// 并下推到未被更深层路由隔断的非控制后代。
		v.gamma = inherited
		t.fixGamma(v.child[0], inherited)
		t.fixGamma(v.child[1], inherited)
		t.recompute(v)
	} else {
		// v 是叶子:摘除,并向上修剪退化成非控制叶子的祖先。
		if len(path) == 0 {
			t.root = nil
		} else {
			parent := path[len(path)-1]
			removeChild(parent, v)
			for i := len(path) - 1; i >= 0; i-- {
				anc := path[i]
				if anc.control || anc.child[0] != nil || anc.child[1] != nil {
					break
				}
				if i == 0 {
					t.root = nil
				} else {
					removeChild(path[i-1], anc)
				}
			}
		}
	}
	// 自底向上重算(已被摘除的节点重算无害,结果不再被引用)。
	for i := len(path) - 1; i >= 0; i-- {
		t.recompute(path[i])
	}
	return old, true
}

func removeChild(parent, child *node) {
	if parent.child[0] == child {
		parent.child[0] = nil
	} else {
		parent.child[1] = nil
	}
}

// count 返回当前路由对应的最少数据面条目数。
func (t *trie) count() int {
	if t.root == nil {
		return 0
	}
	c := t.root.d
	if _, ok := t.root.cand[noRouteColor]; ok {
		c--
	}
	return c
}

// controlQuery 在控制面上按最长前缀匹配查询 addr 的结果。
func (t *trie) controlQuery(addr uint32) color {
	res := noRouteColor
	v := t.root
	d := 0
	for v != nil {
		if v.control {
			res = v.gamma
		}
		if d == 32 {
			break
		}
		v = v.child[(addr>>(31-d))&1]
		d++
	}
	return res
}
