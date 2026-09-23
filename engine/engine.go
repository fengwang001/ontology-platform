// Package engine 执行编译后模式的匹配：段内线性匹配（最后星号回退），
// 跨段 "**" 用段级可达集合 DP，整体 O(模式长度 × 路径长度)。
package engine

import (
	"ontology/runes"
	"ontology/syntax"
)

// Matcher 绑定一个编译模式并保存最近一次匹配的基本比较步数。
type Matcher struct {
	p     *syntax.Pattern
	steps int
}

// New 基于编译模式构造匹配器。
func New(p *syntax.Pattern) *Matcher { return &Matcher{p: p} }

// Compile 编译并构造匹配器（便捷构造）。
func Compile(pattern string) (*Matcher, error) {
	p, err := syntax.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return New(p), nil
}

// MatchString 匹配字符串路径。
func (m *Matcher) MatchString(path string) bool { return m.Match([]byte(path)) }

// Steps 返回最近一次 Match 的基本比较步数（原子与码点的实际比较次数）。
func (m *Matcher) Steps() int { return m.steps }

func splitSegments(path []byte) [][]runes.Rune {
	n := 1
	for _, b := range path {
		if b == '/' {
			n++
		}
	}
	segs := make([][]runes.Rune, n)
	idx := 0
	runes.Each(path, func(r runes.Rune) bool {
		if !r.Bad && r.R == '/' {
			idx++
		} else {
			segs[idx] = append(segs[idx], r)
		}
		return true
})
	return segs
}

// Match 匹配字节路径。空模式只匹配空串。
func (m *Matcher) Match(path []byte) bool {
	m.steps = 0
	if m.p.Empty {
		return len(path) == 0
	}
	psegs := m.p.Segments
	qsegs := splitSegments(path)
	// reach[j]：处理完当前前缀路径段后，模式段 j 可达。
	reach := make([]bool, len(psegs)+1)
	reach[0] = true
	for _, ps := range psegs {
		if ps.DoubleStar {
			closure(reach)
		} else {
			break // 开头连续的 ** 闭包之后，普通段尚未消费任何路径段
		}
	}
	for qi, qseg := range qsegs {
		next := make([]bool, len(psegs)+1)
		for j, ps := range psegs {
			if !reach[j] {
				continue
			}
			if ps.DoubleStar {
				next[j] = true // ** 再吃一个整段
				if j+1 < len(psegs) && !psegs[j+1].DoubleStar && m.matchSeg(psegs[j+1], qseg) {
					next[j+1] = true
				}
			} else if m.matchSeg(ps, qseg) {
				next[j+1] = true
			}
		}
		// next[j] 因 ** 到达后，连续 ** 段可零段推进，并可立刻尝试后续普通段。
		for j := 0; j < len(psegs); j++ {
			if next[j] && psegs[j].DoubleStar {
				propagate(next, psegs, j, qseg, m)
			}
		}
		_ = qi
		reach = next
	}
	// 收尾：剩余全是 **（吃 0 段）即可。
	j := len(psegs)
	for j > 0 && psegs[j-1].DoubleStar {
		if reach[j-1] {
			return true
		}
		j--
	}
	return reach[len(psegs)]
}

func closure(reach []bool) {
	// 仅在初始化使用；调用方逐段处理，这里留空占位避免误用。
}

func propagate(next []bool, psegs []syntax.Segment, j int, qseg []runes.Rune, m *Matcher) {
	for k := j; k < len(psegs) && psegs[k].DoubleStar; k++ {
		next[k] = true
		if k+1 < len(psegs) && !psegs[k+1].DoubleStar && m.matchSeg(psegs[k+1], qseg) {
			next[k+1] = true
		}
	}
}

// matchSeg 段内匹配：最后一个星号回退，线性时间。
func (m *Matcher) matchSeg(seg syntax.Segment, q []runes.Rune) bool {
	atoms := seg.Atoms
	pi, qi := 0, 0
	starP, starQ := -1, -1
	for qi < len(q) {
		if pi < len(atoms) && m.consume(atoms[pi], q[qi]) {
			pi++
			qi++
			continue
		}
		if pi < len(atoms) && atoms[pi].Kind == syntax.AtomStar {
			starP = pi
			starQ = qi
			pi++
			continue
		}
		if starP >= 0 {
			pi = starP + 1
			starQ++
			qi = starQ
			continue
		}
		return false
	}
	for pi < len(atoms) && atoms[pi].Kind == syntax.AtomStar {
		pi++
	}
	return pi == len(atoms)
}

func (m *Matcher) consume(a syntax.Atom, r runes.Rune) bool {
	switch a.Kind {
	case syntax.AtomQuestion:
		m.steps++
		return true
	case syntax.AtomLiteral:
		m.steps++
		return runes.Equal(a.Lit, r)
	case syntax.AtomClass:
		m.steps++
		return a.Class.Match(r)
	default:
		return false
	}
}
