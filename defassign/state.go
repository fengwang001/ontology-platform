package defassign

// 路径标签（诊断依据的规范化词汇），分析器与朴素模型共用同一组词，
// 保证同一读取点两侧模型产出逐字节相同的依据。
const (
	tagIfThen     = "if.then"
	tagIfElse     = "if.else"
	tagLoopZero   = "loop.zero"
	tagLoopIter   = "loop.iter"
	tagBreak      = "break"
	tagReturn     = "return"
	tagHandler    = "handler"
	tagUncaught   = "try.uncaught"
	tagNormalBody = "try.normal"
	tagCleanup    = "cleanup"
	tagHandlerEnd = "handler.end"
	tagViaBreak   = "via.break"
	tagViaReturn  = "via.return"
)

// assigned 是「确定已赋值」的变量集合：对所有到达当前点的执行路径都已赋值。
// 表示为不可变的 map（分叉时显式 copy），汇合 = 交集。
// 交集只遍历较小的一侧，开销与「实际参与该汇合的变量数」相关，
// 与程序中未涉及该汇合的变量数量无关。
type assigned map[string]bool

func emptyAssigned() assigned { return assigned{} }

func (a assigned) clone() assigned {
	b := make(assigned, len(a))
	for k := range a {
		b[k] = true
	}
	return b
}

// withAssign 返回「再赋值一个变量」后的集合。
// 顺序传播处允许原地修改并复用同一 map（调用者保证尚未分叉共享）。
func (a assigned) withAssign(v string, mutate bool) assigned {
	if mutate {
		a[v] = true
		return a
	}
	b := a.clone()
	b[v] = true
	return b
}

// intersectAll 求多侧汇合。返回复用的入参 map（第一次拷贝后直接写），
// 每侧只遍历较小集合，总开销只涉及实际出现在汇合处的变量。
// 无侧时返回 nil 表示「没有任何路径到达」（不可达）。
func intersectAll(sides []assigned) assigned {
	if len(sides) == 0 {
		return nil
	}
	best := 0
	for i := 1; i < len(sides); i++ {
		if len(sides[i]) < len(sides[best]) {
			best = i
		}
	}
	res := sides[best].clone()
	for i, s := range sides {
		if i == best {
			continue
		}
		if len(s) < len(res) {
			next := make(assigned, len(s))
			for k := range s {
				if res[k] {
					next[k] = true
				}
			}
			res = next
		} else {
			for k := range res {
				if !s[k] {
					delete(res, k)
				}
			}
		}
	}
	return res
}

// pending 是「可能仍待读取」的赋值点表：变量 -> 最近一次赋值点集合。
// 一次赋值之后，若从该赋值点出发的所有路径上、在该变量被再次赋值前
// 都没有读取，则它是无效赋值。汇合处取并集（跨路径合并候选）。
type pending map[string]map[Pos]bool

// life 是无效赋值分析的 may 状态：
//   - pend：变量 -> 当前可能是「最近一次赋值」、其后尚未被读取的赋值点；
//   - gone：已经被同变量后续赋值覆盖的赋值点（仍可能被更早的读取见证，
//     故是否无效在出口与 alive 对照后统一判定）。
//
// 两个分量都在分叉处复制、汇合处取并。
type life struct {
	pend pending
	gone map[Pos]bool
}

func newLife() life {
	return life{pend: pending{}, gone: map[Pos]bool{}}
}

func (l life) clone() life {
	g := make(map[Pos]bool, len(l.gone))
	for k := range l.gone {
		g[k] = true
	}
	return life{pend: l.pend.clone(), gone: g}
}

func (l life) put(v string, at Pos) {
	for q := range l.pend[v] {
		l.gone[q] = true
	}
	l.pend[v] = map[Pos]bool{at: true}
}

func (l life) read(v string) []Pos {
	return l.pend.consume(v)
}

func (l life) cloneLife() life { return l.clone() }

func (l life) union(o life) {
	l.pend.unionInto(o.pend)
	for q := range o.gone {
		l.gone[q] = true
	}
}

func unionLife(sides []life) life {
	r := newLife()
	for _, s := range sides {
		r.union(s)
	}
	return r
}

func equalLife(a, b life) bool {
	if len(a.gone) != len(b.gone) {
		return false
	}
	for q := range a.gone {
		if !b.gone[q] {
			return false
		}
	}
	return equalsPending(a.pend, b.pend)
}

func (p pending) clone() pending {
	b := make(pending, len(p))
	for k, set := range p {
		ns := make(map[Pos]bool, len(set))
		for q := range set {
			ns[q] = true
		}
		b[k] = ns
	}
	return b
}

// putAssign 记录一次新赋值：该变量之前待读的赋值点被覆盖（顺序路径上）。
func (p pending) putAssign(v string, at Pos) {
	p[v] = map[Pos]bool{at: true}
}

// putAssignGone 同 putAssign，但把被覆盖的旧赋值点收入 gone（may 集合）。
func (p pending) putAssignGone(v string, at Pos, gone map[Pos]bool) {
	for q := range p[v] {
		gone[q] = true
	}
	p[v] = map[Pos]bool{at: true}
}

// consume 记录一次读取：清空该变量所有待读赋值点，并返回被证伪（有效）的赋值点。
func (p pending) consume(v string) []Pos {
	set := p[v]
	if len(set) == 0 {
		return nil
	}
	out := make([]Pos, 0, len(set))
	for q := range set {
		out = append(out, q)
	}
	delete(p, v)
	return out
}

// canThrowAt 报告该语句边界是否可能抛出（从而产生「中途抛出」车道）。
// 声明、跳出、返回与结构标记本身不抛出；只有 assign/use 是抛出点
// （if/loop/try 内部的抛出点在遍历其内部语句时另行统计）。
func canThrowAt(k Kind) bool {
	return k == KAssign || k == KUse
}

// cleanupThrowBorders 静态统计清理区域内可能抛出、并会逃逸到外层保护结构
// 的语句边界数：普通 assign/use 算一个；嵌套 try 的清理区域也算，
// 嵌套 try 的被保护体/分支抛出由内层自行接住，不算。
func cleanupThrowBorders(stmts []*Stmt) int {
	n := 0
	var walk func([]*Stmt)
	walk = func(ss []*Stmt) {
		for _, s := range ss {
			switch s.Kind {
			case KAssign, KUse:
				n++
			case KTry:
				walk(s.Cleanup)
			case KIf:
				walk(s.Body)
				walk(s.Else)
			case KLoop:
				walk(s.Body)
			}
		}
	}
	walk(stmts)
	return n
}

// unionInto 把 other 并入 p（顺序路径上原地修改）；内层集合逐项拷贝，
// 避免分叉后的多条路径共享同一个 set 而互相覆盖。
func (p pending) unionInto(other pending) {
	for k, set := range other {
		dst := p[k]
		if dst == nil {
			dst = make(map[Pos]bool, len(set))
			p[k] = dst
		}
		for q := range set {
			dst[q] = true
		}
	}
}

func unionAll(sides []pending) pending {
	res := pending{}
	for _, s := range sides {
		res.unionInto(s)
	}
	return res
}

// equalsPending 用于循环固定点判定。
func equalsPending(a, b pending) bool {
	if len(a) != len(b) {
		return false
	}
	for k, sa := range a {
		sb := b[k]
		if len(sa) != len(sb) {
			return false
		}
		for q := range sa {
			if !sb[q] {
				return false
			}
		}
	}
	return true
}
