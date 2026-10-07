package subtype

// Stats 记录一次判定的可验证统计信息。
type Stats struct {
	// DistinctPairs 判定过程中实际检查过的不同 (左名, 右名) 命名对数量。
	DistinctPairs int
	// PairChecks 命名对检查的总次数（含缓存失效后的重查）。
	PairChecks int
	// ReachableLeft 左类型静态可达的命名类型数量。
	ReachableLeft int
	// ReachableRight 右类型静态可达的命名类型数量。
	ReachableRight int
	// ReachableUnion 两侧可达命名类型的并集大小。
	ReachableUnion int
}

// PairBound 返回命名对数量的上界。
//
// 由于可写属性的不变性检查会交换左右两侧，命名对的两个分量
// 都可能来自任一侧，因此上界取两侧可达名字并集的两两组合总数。
func (s Stats) PairBound() int {
	return s.ReachableUnion * s.ReachableUnion
}

// namePair 是一对 (左名, 右名) 命名类型引用，是共归纳假设与缓存的键。
type namePair struct {
	left  string
	right string
}

// frame 是假设栈上的一帧：记录正在判定中的命名对，
// 以及该帧打开期间写入缓存的条目（用于假设被推翻时回滚）。
type frame struct {
	pair namePair
	adds []namePair
}

// checker 是一次判定（在一份定义快照上）的状态。
//
// 正确性论据（最大解 / 共归纳）：
//   - 只有 (Ref, Ref) 对可能成环：非引用侧只做结构分解，严格变小；
//     单侧引用展开后另一侧仍严格变小，而无保护循环已在判定前被拒绝，
//     因此任何无限下降都必须重复某个 (Ref, Ref) 对。
//   - 遇到假设栈中尚未得出结论的对时返回 true（共归纳假设）。
//   - 假设帧关闭时若结果为 false，则该帧期间写入缓存的所有条目
//     都可能依赖了被推翻的假设，必须一并作废；若结果为 true，
//     假设得到证实，这些条目并入父帧（对更外层假设仍是暂定的）。
type checker struct {
	defs    map[string]Type
	cache   map[namePair]bool
	onStack map[namePair]bool
	stack   []frame
	seen    map[namePair]bool
	stats   *Stats
}

// subtypeOf 在 defs 快照上判定 l 是否为 r 的子类型，并记录统计信息。
func subtypeOf(defs map[string]Type, l, r Type, stats *Stats) bool {
	c := &checker{
		defs:    defs,
		cache:   make(map[namePair]bool),
		onStack: make(map[namePair]bool),
		seen:    make(map[namePair]bool),
		stats:   stats,
	}
	return c.check(l, r)
}

// check 判定 l 是否为 r 的子类型。l、r 均非 nil 且已定义引用均已展开校验。
func (c *checker) check(l, r Type) bool {
	// 顶类型与底类型规则优先。
	if _, ok := r.(Top); ok {
		return true
	}
	if _, ok := l.(Bottom); ok {
		return true
	}
	// 联合规则：左侧联合要求每个成员都是右侧的子类型（空联合即底类型，
	// 空量化成立）；右侧联合要求存在某个成员（空联合无成员可选，不成立）。
	if lu, ok := l.(Union); ok {
		for _, m := range lu.Members {
			if !c.check(m, r) {
				return false
			}
		}
		return true
	}
	if ru, ok := r.(Union); ok {
		for _, m := range ru.Members {
			if c.check(l, m) {
				return true
			}
		}
		return false
	}
	// 引用规则。
	lr, lIsRef := l.(Ref)
	rr, rIsRef := r.(Ref)
	switch {
	case lIsRef && rIsRef:
		if lr.Name == rr.Name {
			return true
		}
		return c.checkPair(lr.Name, rr.Name)
	case lIsRef:
		return c.check(c.defs[lr.Name], r)
	case rIsRef:
		return c.check(l, c.defs[rr.Name])
	}
	// 结构规则。
	switch lt := l.(type) {
	case Int:
		switch r.(type) {
		case Int, Float:
			return true
		}
		return false
	case Float:
		_, ok := r.(Float)
		return ok
	case Str:
		_, ok := r.(Str)
		return ok
	case Bool:
		_, ok := r.(Bool)
		return ok
	case Top:
		return false // r 为 Top 的情况已在上面处理。
	case Object:
		rt, ok := r.(Object)
		if !ok {
			return false
		}
		return c.checkObject(lt, rt)
	case Func:
		rt, ok := r.(Func)
		if !ok {
			return false
		}
		return c.checkFunc(lt, rt)
	}
	return false
}

// checkPair 判定命名对 (left, right) 的子类型关系，带共归纳假设与缓存。
func (c *checker) checkPair(left, right string) bool {
	p := namePair{left: left, right: right}
	if v, ok := c.cache[p]; ok {
		return v
	}
	if c.onStack[p] {
		// 正在判定且尚未得出结论的同一对：视为成立（共归纳假设）。
		return true
	}
	if !c.seen[p] {
		c.seen[p] = true
		c.stats.DistinctPairs++
	}
	c.stats.PairChecks++

	c.onStack[p] = true
	c.stack = append(c.stack, frame{pair: p})
	result := c.check(c.defs[left], c.defs[right])
	top := c.stack[len(c.stack)-1]
	c.stack = c.stack[:len(c.stack)-1]
	delete(c.onStack, p)

	if result {
		// 假设成立：本帧期间的所有缓存条目得到证实，并入父帧。
		c.commit(p, true)
		for _, q := range top.adds {
			c.commit(q, c.cache[q])
		}
	} else {
		// 假设被推翻：作废本帧期间写入的全部缓存条目；
		// p=false 本身不依赖 p 的假设（对 p 是乐观假设，失败即确证），
		// 但对更外层的假设仍是暂定的，记入父帧。
		for _, q := range top.adds {
			delete(c.cache, q)
		}
		c.recordToParent(p)
		c.cache[p] = false
	}
	return result
}

// commit 将 (p, v) 写入缓存并记入父帧的暂定条目。
func (c *checker) commit(p namePair, v bool) {
	c.cache[p] = v
	c.recordToParent(p)
}

// recordToParent 把 p 记入当前打开的假设帧（若有），表示该缓存条目
// 的有效性依赖于这些尚未关闭的假设。
func (c *checker) recordToParent(p namePair) {
	if len(c.stack) > 0 {
		top := &c.stack[len(c.stack)-1]
		top.adds = append(top.adds, p)
	}
}

// checkObject 判定对象子类型：宽度（S 可有额外属性）与深度（逐属性比较）。
func (c *checker) checkObject(s, t Object) bool {
	sProps := make(map[string]Prop, len(s.Props))
	for _, p := range s.Props {
		sProps[p.Name] = p
	}
	for _, tp := range t.Props {
		sp, ok := sProps[tp.Name]
		if !ok {
			// T 的必选属性在 S 中必须存在；可选属性可缺失。
			if !tp.Optional {
				return false
			}
			continue
		}
		// T 的必选属性在 S 中必须为必选；T 的可选属性在 S 中可选或必选均可。
		if !tp.Optional && sp.Optional {
			return false
		}
		if tp.ReadOnly {
			// T 只读：S 中该属性是否只读不限，类型只需是子类型。
			if !c.check(sp.Type, tp.Type) {
				return false
			}
			continue
		}
		// T 可写：S 中该属性必须也可写，且两侧类型互为子类型（不变）。
		if sp.ReadOnly {
			return false
		}
		if !c.check(sp.Type, tp.Type) || !c.check(tp.Type, sp.Type) {
			return false
		}
	}
	return true
}

// checkFunc 判定函数子类型：S 的参数个数不多于 T，参数逆变，返回值协变。
func (c *checker) checkFunc(s, t Func) bool {
	if len(s.Params) > len(t.Params) {
		return false
	}
	for i, sp := range s.Params {
		if !c.check(t.Params[i], sp) {
			return false
		}
	}
	return c.check(s.Return, t.Return)
}
