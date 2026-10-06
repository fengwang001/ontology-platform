package subtype

// pair 是一对待判定的（左类型，右类型）。
type pair struct {
	s, t *Type
}

// node 是一对类型的判定需求：常量部分 ok 与若干"合取子对"的析取。
// 该对成立当且仅当 ok 为真，且 alts 中至少一个合取项的全部子对成立；
// alts 为空时仅由 ok 决定。
type node struct {
	ok   bool
	alts [][]pair
}

// engine 在有限的对集合上计算最大不动点。
//
// 关键不变量：判定 (S, T) 时发现的每一对 (s, t)，其左侧必属于 S 的
// 静态闭包 V(S)，右侧必属于 T 的静态闭包 V(T)，因此对集合
// P 是 V(S) × V(T) 的子集，不同命名对数量不超过 |names(S)| × |names(T)|。
type engine struct {
	defs  map[string]*Type
	nodes map[pair]*node
}

func newEngine(defs map[string]*Type) *engine {
	return &engine{defs: defs}
}

// build 根据结构规则构造一对类型的判定需求。调用前保证：
// 两侧表达式结构合法、所有可达引用均已登记且无无保护循环。
func (e *engine) build(s, t *Type) *node {
	switch {
	case t.Kind == KindTop:
		return &node{ok: true}
	case s.Kind == KindBottom:
		return &node{ok: true}
	case s.Kind == KindRef && t.Kind == KindRef && s.Name == t.Name:
		return &node{ok: true}
	case s.Kind == KindRef:
		return &node{ok: true, alts: [][]pair{{{e.defs[s.Name], t}}}}
	case t.Kind == KindRef:
		return &node{ok: true, alts: [][]pair{{{s, e.defs[t.Name]}}}}
	case s.Kind == KindUnion:
		alt := make([]pair, len(s.Members))
		for i, m := range s.Members {
			alt[i] = pair{m, t}
		}
		return &node{ok: true, alts: [][]pair{alt}}
	case t.Kind == KindUnion:
		alts := make([][]pair, len(t.Members))
		for i, m := range t.Members {
			alts[i] = []pair{{s, m}}
		}
		return &node{ok: true, alts: alts}
	}
	switch s.Kind {
	case KindInt, KindFloat, KindString, KindBool:
		return &node{ok: primLE(s.Kind, t.Kind)}
	case KindObject:
		if t.Kind != KindObject {
			return &node{ok: false}
		}
		return objectNode(s, t)
	case KindFunc:
		if t.Kind != KindFunc {
			return &node{ok: false}
		}
		return funcNode(s, t)
	}
	return &node{ok: false}
}

// objectNode 实现对象规则：T 的每个属性在 S 中被满足。
// 存在性/可选性/只读性归入常量部分，属性类型约束归入合取子对。
func objectNode(s, t *Type) *node {
	n := &node{ok: true}
	var alt []pair
	for _, pt := range t.Props {
		ps, found := findProp(s, pt.Name)
		if !found {
			if !pt.Optional {
				n.ok = false
			}
			continue
		}
		if !pt.Optional && ps.Optional {
			n.ok = false
			continue
		}
		if pt.ReadOnly {
			alt = append(alt, pair{ps.Type, pt.Type})
		} else {
			if ps.ReadOnly {
				n.ok = false
				continue
			}
			alt = append(alt, pair{ps.Type, pt.Type}, pair{pt.Type, ps.Type})
		}
	}
	if len(alt) > 0 {
		n.alts = [][]pair{alt}
	}
	return n
}

// funcNode 实现函数规则：S 参数不多于 T，参数逆变，返回协变。
func funcNode(s, t *Type) *node {
	if len(s.Params) > len(t.Params) {
		return &node{ok: false}
	}
	n := &node{ok: true}
	alt := make([]pair, 0, len(s.Params)+1)
	for i, sp := range s.Params {
		alt = append(alt, pair{t.Params[i], sp})
	}
	alt = append(alt, pair{s.Ret, t.Ret})
	n.alts = [][]pair{alt}
	return n
}

// discover 从根对出发做广度优先搜索，枚举所有可达对及其需求。
func (e *engine) discover(s, t *Type) {
	e.nodes = map[pair]*node{}
	queue := []pair{{s, t}}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if _, seen := e.nodes[p]; seen {
			continue
		}
		n := e.build(p.s, p.t)
		e.nodes[p] = n
		for _, alt := range n.alts {
			for _, q := range alt {
				if _, seen := e.nodes[q]; !seen {
					queue = append(queue, q)
				}
			}
		}
	}
}

// run 计算最大不动点并返回根对的真值与统计信息。
//
// 从"全部对成立"的顶元素出发反复应用单调算子 F，直到稳定。
// 结果即满足全部结构规则的最大关系（共归纳/最大解语义），
// 不存在"假设被推翻后仍被复用"的中间结论。
func (e *engine) run(s, t *Type) (bool, Stats) {
	e.discover(s, t)
	sat := make(map[pair]bool, len(e.nodes))
	named := map[[2]string]bool{}
	for p := range e.nodes {
		sat[p] = true
		if p.s.Kind == KindRef && p.t.Kind == KindRef {
			named[[2]string{p.s.Name, p.t.Name}] = true
		}
	}
	iterations := 0
	for {
		changed := false
		iterations++
		for p, n := range e.nodes {
			v := eval(n, sat)
			if v != sat[p] {
				sat[p] = v
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	stats := Stats{
		PairsDiscovered: len(e.nodes),
		NamedPairs:      len(named),
		Iterations:      iterations,
	}
	return sat[pair{s, t}], stats
}

// eval 在当前假设下求值一个节点。
func eval(n *node, sat map[pair]bool) bool {
	if !n.ok {
		return false
	}
	if len(n.alts) == 0 {
		return true
	}
	for _, alt := range n.alts {
		all := true
		for _, q := range alt {
			if !sat[q] {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}
