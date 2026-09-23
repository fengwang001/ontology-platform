// Package engine 执行匹配：段内 */?/字符类，跨段 **。
// 算法为段级 NFA 状态集模拟，复杂度 O(模式原子数 × 路径码点数)，无指数回溯。
package engine

import (
	"strings"
	"sync/atomic"

	"ontology/runes"
	"ontology/syntax"
)

// Matcher 是编译后模式的可并发匹配器。
type Matcher struct {
	pat   *syntax.Pattern
	base  []int // 每段的状态起始编号
	segOf []int // 状态编号 → 段下标
	n     int   // 状态总数
	steps atomic.Int64
	sufOK []bool // sufOK[i]: 第 i 段之后全为 ** 段（都可匹配零段）
}

// New 由编译好的模式构造匹配器。
func New(p *syntax.Pattern) *Matcher {
	m := &Matcher{pat: p}
	ok := true
	for i, s := range p.Segments {
		m.base = append(m.base, m.n)
		for j := 0; j <= len(s.Atoms); j++ {
			m.segOf = append(m.segOf, i)
			m.n++
		}
	}
	m.sufOK = make([]bool, len(p.Segments))
	for i := len(p.Segments) - 1; i >= 0; i-- {
		m.sufOK[i] = ok
		ok = ok && p.Segments[i].DoubleStar
	}
	return m
}

// Steps 返回最近一次 Match 执行的基本比较步数。
func (m *Matcher) Steps() int64 { return m.steps.Load() }

// Match 判断 path 是否匹配模式。
func (m *Matcher) Match(path string) bool {
	var steps int64
	set := make([]bool, m.n)
	cur := m.add(nil, set, 0)
	cur = m.closure(cur, set)
	nextSet := make([]bool, m.n)
	segs := strings.Split(path, "/")
	for si, seg := range segs {
		if si > 0 {
			cur = m.boundary(cur, set)
		}
		b := []byte(seg)
		for i := 0; i < len(b); {
			t := runes.Decode(b, i)
			i += t.Size
			var next []int
			for _, s := range cur {
				si := m.segOf[s]
				segp := &m.pat.Segments[si]
				if segp.DoubleStar {
					steps++ // ** 自环消费一段内码点
					next = m.add(next, nextSet, s)
					continue
				}
				j := s - m.base[si]
				if j >= len(segp.Atoms) {
					continue
				}
				a := segp.Atoms[j]
				steps++ // 原子与码点的一次基本比较
				if matchAtom(a, t) {
					next = m.add(next, nextSet, s+1)
				}
				if a.Kind == syntax.Star {
					next = m.add(next, nextSet, s)
				}
			}
			next = m.closure(next, nextSet)
			for _, s := range cur {
				set[s] = false
			}
			cur, set, nextSet = next, nextSet, set
		}
	}
	ok := false
	for _, s := range cur {
		si := m.segOf[s]
		if s-m.base[si] == len(m.pat.Segments[si].Atoms) && m.sufOK[si] {
			ok = true
			break
		}
	}
	m.steps.Store(steps)
	return ok
}

func (m *Matcher) add(list []int, set []bool, s int) []int {
	if !set[s] {
		set[s] = true
		list = append(list, s)
	}
	return list
}

// boundary 跨段：消费完一个路径段后，只有段完整的状态能进入下一段；
// ** 段的状态保留（继续消费后续段）。随后求 ε 闭包。
func (m *Matcher) boundary(cur []int, set []bool) []int {
	nextSet := make([]bool, m.n)
	var next []int
	for _, s := range cur {
		si := m.segOf[s]
		segp := &m.pat.Segments[si]
		if segp.DoubleStar {
			next = m.add(next, nextSet, s) // ** 继续消费下一段
		}
		if s-m.base[si] == len(segp.Atoms) && si+1 < len(m.pat.Segments) {
			next = m.add(next, nextSet, m.base[si+1])
		}
	}
	for _, s := range cur {
		set[s] = false
	}
	copy(set, nextSet)
	return m.closure(next, set)
}

// closure 计算 ε 闭包：星号跳过（* 匹配零个码点）与 ** 退出（** 匹配零段）。
func (m *Matcher) closure(list []int, set []bool) []int {
	for k := 0; k < len(list); k++ {
		s := list[k]
		si := m.segOf[s]
		segp := &m.pat.Segments[si]
		j := s - m.base[si]
		if !segp.DoubleStar && j < len(segp.Atoms) && segp.Atoms[j].Kind == syntax.Star {
			list = m.add(list, set, s+1)
		}
		if segp.DoubleStar && si+1 < len(m.pat.Segments) {
			list = m.add(list, set, m.base[si+1])
		}
	}
	return list
}

func matchAtom(a syntax.Atom, t runes.Token) bool {
	switch a.Kind {
	case syntax.Lit:
		return runes.Equal(a.Lit, t)
	case syntax.Any, syntax.Star:
		return true
	case syntax.Class:
		return a.Class.Match(t)
	}
	return false
}
