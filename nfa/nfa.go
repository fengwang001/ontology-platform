// Package nfa 用 Thompson 构造把 reast.RE 编译成带 ε 转移的 NFA，并以 ε 闭包匹配。
package nfa

import "ontology/reast"

// Edge 是一条转移 From -Ch-> To；Ch==0 表示 ε（字符仅 a-z，不会为 0）。
// Char 表里的边 From 即其下标状态，冗余存储仅为统一构造轨迹的表示。
type Edge struct {
	From, To int
	Ch       byte
}

// Step 记录 Thompson 构造的一步：操作名、新建状态、新增边。
type Step struct {
	Op    string
	New   []int
	Edges []Edge
}

// NFA 是 Thompson 构造产物；Char/Eps 下标即状态号。
// touched 为非导出计数器，只在 appendLit 路径记录触碰的既有状态数。
type NFA struct {
	Char    [][]Edge
	Eps     [][]int
	Start   int
	Accept  int
	touched int
}

type frag struct{ start, accept int }
type builder struct {
	n     *NFA
	steps []Step
}

func (n *NFA) add() int {
	n.Char = append(n.Char, nil)
	n.Eps = append(n.Eps, nil)
	return len(n.Char) - 1
}
func (b *builder) eps(f, t int) { b.n.Eps[f] = append(b.n.Eps[f], t) }
func (b *builder) log(op string, neww []int, es ...Edge) {
	b.steps = append(b.steps, Step{op, neww, es})
}

// build 先构造子节点再申请新状态，使状态号严格按构造顺序连续增长。
func (b *builder) build(x *reast.Node) frag {
	switch x.Kind {
	case reast.Lit:
		s, f := b.n.add(), b.n.add()
		b.n.Char[s] = append(b.n.Char[s], Edge{s, f, x.Ch})
		b.log("char "+string(x.Ch), []int{s, f}, Edge{s, f, x.Ch})
		return frag{s, f}
	case reast.Eps:
		s, f := b.n.add(), b.n.add()
		b.eps(s, f)
		b.log("eps", []int{s, f}, Edge{s, f, 0})
		return frag{s, f}
	case reast.Cat: // A·B：加 A.accept -ε-> B.start，start=A.start，accept=B.accept
		a := b.build(x.L)
		c := b.build(x.R)
		b.eps(a.accept, c.start)
		b.log("concat", nil, Edge{a.accept, c.start, 0})
		return frag{a.start, c.accept}
	case reast.Alt: // A|B：新建 s,f 与四条 ε 边
		a := b.build(x.L)
		c := b.build(x.R)
		s, f := b.n.add(), b.n.add()
		b.eps(s, a.start)
		b.eps(s, c.start)
		b.eps(a.accept, f)
		b.eps(c.accept, f)
		b.log("alt", []int{s, f}, Edge{s, a.start, 0}, Edge{s, c.start, 0}, Edge{a.accept, f, 0}, Edge{c.accept, f, 0})
		return frag{s, f}
	default: // reast.Star：新建 s,f，四条 ε 边（含回边与直达 f）
		a := b.build(x.L)
		s, f := b.n.add(), b.n.add()
		b.eps(s, a.start)
		b.eps(s, f)
		b.eps(a.accept, a.start)
		b.eps(a.accept, f)
		b.log("star", []int{s, f}, Edge{s, a.start, 0}, Edge{s, f, 0}, Edge{a.accept, a.start, 0}, Edge{a.accept, f, 0})
		return frag{s, f}
	}
}

func compile(re *reast.RE) (*NFA, []Step) {
	b := &builder{n: &NFA{}}
	fr := b.build(re.Root)
	b.n.Start, b.n.Accept = fr.start, fr.accept
	return b.n, b.steps
}

// Compile 按 Thompson 构造把 AST 编译成 NFA。
func Compile(re *reast.RE) *NFA { n, _ := compile(re); return n }

// CompileTrace 同 Compile，额外按构造顺序返回每一步（逐步演示用；不含计数器）。
func CompileTrace(re *reast.RE) (*NFA, []Step) { return compile(re) }
func (n *NFA) Append(c byte) {
	n.touched = 0 // 以 ε 边连接新字符片段；只触碰原 Accept 一个既有状态
	old := n.Accept
	s, f := n.add(), n.add()
	n.Char[s] = append(n.Char[s], Edge{s, f, c})
	n.Eps[old] = append(n.Eps[old], s)
	n.touched++ // 读取/改动的既有状态仅 old 一处，与既有规模无关
	n.Accept = f
}

// closure 返回 seeds 沿 ε 边的传递闭包（不重不漏）。
func closure(n *NFA, seeds map[int]struct{}) map[int]struct{} {
	r := map[int]struct{}{}
	q := make([]int, 0, len(seeds))
	for s := range seeds {
		r[s] = struct{}{}
		q = append(q, s)
	}
	for len(q) > 0 {
		x := q[0]
		q = q[1:]
		for _, t := range n.Eps[x] {
			if _, ok := r[t]; !ok {
				r[t] = struct{}{}
				q = append(q, t)
			}
		}
	}
	return r
}

// After 返回 Match 消费 s 后的状态集（每字符步后均已 ε 闭包）；Match 即查其中是否含接受态。
func (n *NFA) After(s string) map[int]struct{} {
	cur := closure(n, map[int]struct{}{n.Start: {}})
	for k := 0; k < len(s); k++ {
		nxt := map[int]struct{}{}
		for q := range cur {
			for _, e := range n.Char[q] {
				if e.Ch == s[k] {
					nxt[e.To] = struct{}{}
				}
			}
		}
		cur = closure(n, nxt)
	}
	return cur
}

// Match 从起点 ε 闭包逐字符推进，读完时当前集含接受态即接受；全程只读。
func (n *NFA) Match(s string) bool { _, ok := n.After(s)[n.Accept]; return ok }
