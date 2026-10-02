package matcher

import "strings"

// node 是展开后无 Or 的模式节点。
type node struct {
	wild bool
	ctor *ctorInfo
	args []*node
}

var wildNode = &node{wild: true}

// rpat 是类型校验后的模式（Or 保留）。
type rpat struct {
	kind PatKind
	ctor *ctorInfo
	args []*rpat
	alts []*rpat
}

// expCap 是展开计数截断上限（足够判定两类展开上限）。
const expCap = maxSessionExpansion + 1

// expCount 计算模式展开行数：通配为 1，构造子为各子模式之积，Or 为各分支之和。
func expCount(r *rpat) int {
	switch r.kind {
	case WildPat:
		return 1
	case OrPat:
		sum := 0
		for _, a := range r.alts {
			sum += expCount(a)
			if sum > expCap {
				return expCap
			}
		}
		return sum
	default: // CtorPat
		prod := 1
		for _, a := range r.args {
			prod *= expCount(a)
			if prod > expCap {
				return expCap
			}
		}
		return prod
	}
}

// expandPat 把校验后的模式按从左到右先序展开成无 Or 的模式序列。
func expandPat(r *rpat) []*node {
	switch r.kind {
	case WildPat:
		return []*node{wildNode}
	case OrPat:
		var out []*node
		for _, a := range r.alts {
			out = append(out, expandPat(a)...)
		}
		return out
	default: // CtorPat
		combos := [][]*node{{}}
		for _, a := range r.args {
			sub := expandPat(a)
			var next [][]*node
			for _, prefix := range combos {
				for _, s := range sub {
					row := make([]*node, 0, len(prefix)+1)
					row = append(row, prefix...)
					row = append(row, s)
					next = append(next, row)
				}
			}
			combos = next
		}
		out := make([]*node, len(combos))
		for i, c := range combos {
			out[i] = &node{ctor: r.ctor, args: c}
		}
		return out
	}
}

// sigma 返回首列出现的构造子集合（通配不计）。
func headSigma(rows [][]*node) map[*ctorInfo]bool {
	s := make(map[*ctorInfo]bool)
	for _, r := range rows {
		if !r[0].wild {
			s[r[0].ctor] = true
		}
	}
	return s
}

// specialize 保留首列为 c 或通配的行：首列换成 c 的各子模式
// （通配换成与字段数等长的通配），再接其余列。
func specialize(rows [][]*node, c *ctorInfo) [][]*node {
	var out [][]*node
	for _, r := range rows {
		h := r[0]
		switch {
		case h.wild:
			row := make([]*node, 0, len(c.fields)+len(r)-1)
			for range c.fields {
				row = append(row, wildNode)
			}
			row = append(row, r[1:]...)
			out = append(out, row)
		case h.ctor == c:
			row := make([]*node, 0, len(h.args)+len(r)-1)
			row = append(row, h.args...)
			row = append(row, r[1:]...)
			out = append(out, row)
		}
	}
	return out
}

// defaultRows 取默认行集：仅首列为通配的行，去掉首列。
func defaultRows(rows [][]*node) [][]*node {
	var out [][]*node
	for _, r := range rows {
		if r[0].wild {
			out = append(out, r[1:])
		}
	}
	return out
}

func tailTypes(types []*typeInfo) []*typeInfo {
	out := make([]*typeInfo, len(types)-1)
	copy(out, types[1:])
	return out
}

func fieldTypes(c *ctorInfo, rest []*typeInfo) []*typeInfo {
	out := make([]*typeInfo, 0, len(c.fields)+len(rest))
	out = append(out, c.fields...)
	out = append(out, rest...)
	return out
}

// missing 按规范过程求反例：返回反例模式序列与是否存在反例。
func missing(rows [][]*node, types []*typeInfo) ([]*node, bool) {
	if len(types) == 0 {
		if len(rows) == 0 {
			return []*node{}, true
		}
		return nil, false
	}
	t := types[0]
	sigma := headSigma(rows)
	if len(sigma) == len(t.ctors) {
		// Σ 含 T 的全部构造子：按声明顺序逐个特化，取第一个有反例的。
		for _, c := range t.ctors {
			ce, ok := missing(specialize(rows, c), fieldTypes(c, tailTypes(types)))
			if !ok {
				continue
			}
			k := len(c.fields)
			res := make([]*node, 0, len(ce)-k+1)
			res = append(res, &node{ctor: c, args: ce[:k]})
			res = append(res, ce[k:]...)
			return res, true
		}
		return nil, false
	}
	// Σ 不完全：取默认行集递归。
	ce, ok := missing(defaultRows(rows), tailTypes(types))
	if !ok {
		return nil, false
	}
	var first *node
	if len(sigma) == 0 {
		first = wildNode
	} else {
		var mc *ctorInfo
		for _, c := range t.ctors {
			if !sigma[c] {
				mc = c
				break
			}
		}
		args := make([]*node, len(mc.fields))
		for i := range args {
			args[i] = wildNode
		}
		first = &node{ctor: mc, args: args}
	}
	res := make([]*node, 0, len(ce)+1)
	res = append(res, first)
	res = append(res, ce...)
	return res, true
}

// covered 判定模式元组 pats 匹配的值是否全部被行集 rows 覆盖。
// pats 与 types 等长且不含 Or；rows 中每行与 types 等长。
func covered(rows [][]*node, pats []*node, types []*typeInfo) bool {
	if len(types) == 0 {
		return len(rows) > 0
	}
	t := types[0]
	p := pats[0]
	if !p.wild {
		// 值的首构造子必为 p.ctor，只有首列为该构造子或通配的行可能覆盖。
		npats := make([]*node, 0, len(p.args)+len(pats)-1)
		npats = append(npats, p.args...)
		npats = append(npats, pats[1:]...)
		return covered(specialize(rows, p.ctor), npats, fieldTypes(p.ctor, tailTypes(types)))
	}
	// p 为通配：对每个构造子分别判定。
	sigma := headSigma(rows)
	for _, c := range t.ctors {
		if sigma[c] {
			npats := make([]*node, 0, len(c.fields)+len(pats)-1)
			for range c.fields {
				npats = append(npats, wildNode)
			}
			npats = append(npats, pats[1:]...)
			if !covered(specialize(rows, c), npats, fieldTypes(c, tailTypes(types))) {
				return false
			}
		} else {
			if !covered(defaultRows(rows), pats[1:], tailTypes(types)) {
				return false
			}
		}
	}
	return true
}

// renderNode 输出反例文本：构造子名后接括号，子项以 ", " 分隔，零字段只写名字。
func renderNode(n *node) string {
	if n.wild {
		return "_"
	}
	if len(n.args) == 0 {
		return n.ctor.name
	}
	parts := make([]string, len(n.args))
	for i, a := range n.args {
		parts[i] = renderNode(a)
	}
	return n.ctor.name + "(" + strings.Join(parts, ", ") + ")"
}
