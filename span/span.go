// Package span 记录原文区间与输出区间的对应关系，支持双向偏移映射。
// 区间分三类：Keep（等长保留）、Del（原文删除，输出长度 0）、Ins（插入，原文长度 0）。
package span

import "sort"

// Kind 标识区间种类。
type Kind uint8

const (
	Keep Kind = iota
	Del
	Ins
)

// Seg 是一个映射区间，坐标左闭右开。
type Seg struct {
	OS, OE int // 原文 [OS,OE)
	NS, NE int // 输出 [NS,NE)
	Kind   Kind
}

// Map 是不可变映射表，查询全部走二分。
type Map struct {
	segs   []Seg
	lastN  int // 最近一次查询检查的区间数
}

// New 由区间切片构造映射表（拷贝并排序）。
func New(segs []Seg) *Map {
	s := append([]Seg(nil), segs...)
	sort.SliceStable(s, func(i, j int) bool {
		if s[i].OS != s[j].OS {
			return s[i].OS < s[j].OS
		}
		return s[i].NS < s[j].NS
	})
	return &Map{segs: s}
}

// Len 返回区间数。
func (m *Map) Len() int { return len(m.segs) }

// LastSteps 返回最近一次 ToOrig/ToOut 查询检查过的区间数（二分步数）。
func (m *Map) LastSteps() int { return m.lastN }

// ToOrig 把输出偏移 o（含终点）映射回原文偏移，单调不减。
func (m *Map) ToOrig(o int) int {
	s := m.segs
	lo, hi, steps := 0, len(s), 0
	for lo < hi {
		steps++
		mid := (lo + hi) / 2
		if o < s[mid].NS {
			hi = mid
		} else if o >= s[mid].NE && s[mid].Kind != Del {
			lo = mid + 1
		} else {
			lo, hi = mid, mid
		}
	}
	m.lastN = steps
	if lo >= len(s) {
		if len(s) > 0 {
			return s[len(s)-1].OE
		}
		return 0
	}
	g := s[lo]
	switch g.Kind {
	case Del:
		return g.OS
	case Ins:
		if lo > 0 {
			return s[lo-1].OE
		}
		return 0
	default:
		return g.OS + (o - g.NS)
	}
}

// ToOut 把原文偏移 i（含终点）映射到输出偏移，单调不减。
func (m *Map) ToOut(i int) int {
	s := m.segs
	lo, hi, steps := 0, len(s), 0
	for lo < hi {
		steps++
		mid := (lo + hi) / 2
		if i < s[mid].OS {
			hi = mid
		} else if i > s[mid].OE || (i == s[mid].OE && s[mid].Kind != Del) {
			lo = mid + 1
		} else {
			lo, hi = mid, mid
		}
}
	m.lastN = steps
	if lo >= len(s) {
		if len(s) > 0 {
			return s[len(s)-1].NE
		}
		return 0
	}
	g := s[lo]
	switch g.Kind {
	case Del:
		return g.NE
	case Ins:
		return g.NS
	default:
		return g.NS + (i - g.OS)
	}
}
