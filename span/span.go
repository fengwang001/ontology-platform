// Package span 记录原文区间与输出区间的对应关系，支持双向偏移查询。
//
// 每个段斜率为 1（复制）或 0（删除/末尾插入）。段按写入顺序排列，
// orig 坐标与 out 坐标各自单调不减。ToOrig/ToOut 均为二分查找。
package span

import "sort"

// seg 是一个对应区间：原文 [a,b) ↔ 输出 [p,q)。
// 复制段 b-a==q-p==1 宽；删除段 q==p；末尾插入段 b==a。
type seg struct {
	a, b, p, q int
}

// Map 是不可变查询视图（由 Builder 构造）。
type Map struct {
	segs      []seg
	lastProbe int
}

// Builder 增量构造 Map。零值即可用。
type Builder struct {
	segs []seg
	a, p int
}

// Copy 记录 n 个原样通过的字节。
func (b *Builder) Copy(n int) {
	if n <= 0 {
		return
	}
	b.segs = appendCopy(b.segs, seg{a: b.a, b: b.a + n, p: b.p, q: b.p + n})
	b.a += n
	b.p += n
}

// Delete 记录 n 个被删原文字节（删除段）。
func (b *Builder) Delete(n int) {
	if n <= 0 {
		return
	}
	b.segs = append(b.segs, seg{a: b.a, b: b.a + n, p: b.p, q: b.p})
	b.a += n
}

// Insert 记录一个无原文对应的输出字节（末尾策略补的 \n）。
func (b *Builder) Insert() {
	b.segs = append(b.segs, seg{a: b.a, b: b.a, p: b.p, q: b.p + 1})
	b.p++
}

func appendCopy(dst []seg, s seg) []seg {
	if n := len(dst); n > 0 {
		if l := dst[n-1]; l.q == s.p && l.b == s.a && l.b-l.a == l.q-l.p {
			dst[n-1].b, dst[n-1].q = s.b, s.q
			return dst
		}
	}
	return append(dst, s)
}

// Merge 把另一个已平移 (origShift,outShift) 的段表并入，合并相邻复制段。
func (b *Builder) Merge(m *Map, origShift, outShift int) {
	for _, s := range m.segs {
		s.a += origShift
		s.b += origShift
		s.p += outShift
		s.q += outShift
		if s.b-s.a == s.q-s.p && s.q-s.p > 0 {
			b.segs = appendCopy(b.segs, s)
		} else {
			b.segs = append(b.segs, s)
		}
	}
	if e := m.OrigEnd(); e+origShift > b.a {
		b.a = e + origShift
	}
	if e := m.OutEnd(); e+outShift > b.p {
		b.p = e + outShift
	}
}

// Build 冻结为查询视图。
func (b *Builder) Build() *Map { return &Map{segs: append([]seg(nil), b.segs...)} }

// OrigEnd 返回原文总长度。
func (m *Map) OrigEnd() int {
	if len(m.segs) == 0 {
		return 0
	}
	return m.segs[len(m.segs)-1].b
}

// OutEnd 返回输出总长度。
func (m *Map) OutEnd() int {
	if len(m.segs) == 0 {
		return 0
	}
	return m.segs[len(m.segs)-1].q
}

// Segments 返回区间总数（只随删除点/插入点增长）。
func (m *Map) Segments() int { return len(m.segs) }

// LastProbes 返回最近一次查询二分检查的区间数。
func (m *Map) LastProbes() int { return m.lastProbe }

// ToOrig 把输出偏移 o（∈[0,OutEnd]）映射为原文偏移。单调不减。
func (m *Map) ToOrig(o int) int {
	m.lastProbe = 0
	i := sort.Search(len(m.segs), func(i int) bool {
		m.lastProbe++
		return m.segs[i].q >= o
	})
	if i == len(m.segs) {
		return m.OrigEnd()
	}
	s := m.segs[i]
	if o >= s.p && o < s.q && s.q-s.p == s.b-s.a {
		return s.a + (o - s.p)
	}
	return s.b
}

// ToOut 把原文偏移 i（∈[0,OrigEnd]）映射为输出偏移。单调不减。
// 被删字节映射到删除段之后的输出偏移（即紧随其后果的位置）。
func (m *Map) ToOut(i int) int {
	m.lastProbe = 0
	idx := sort.Search(len(m.segs), func(k int) bool {
		m.lastProbe++
		return m.segs[k].b >= i
	})
	if idx == len(m.segs) {
		return m.OutEnd()
	}
	s := m.segs[idx]
	if i >= s.a && i < s.b && s.b-s.a == s.q-s.p {
		return s.p + (i - s.a)
	}
	return s.q
}

// Truncate 丢弃输出偏移 outCut 之后的一切，返回裁剪后的新 Map。
// 用于末尾策略：截掉多余尾部 \n。跨复制段时在 cut 处切开。
func (m *Map) Truncate(outCut int) *Map {
	var b Builder
	for _, s := range m.segs {
		if s.p >= outCut {
			break
		}
		if s.q <= outCut {
			if w := s.q - s.p; w == s.b-s.a && w > 0 {
				b.segs = appendCopy(b.segs, s)
			} else {
				b.segs = append(b.segs, s)
			}
			b.a, b.p = s.b, s.q
			continue
		}
		w := outCut - s.p // 落在复制段内部
		b.segs = appendCopy(b.segs, seg{a: s.a, b: s.a + w, p: s.p, q: outCut})
		b.a, b.p = s.a+w, outCut
		break
	}
	return b.Build()
}
