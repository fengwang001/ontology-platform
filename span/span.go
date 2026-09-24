// Package span 记录原文区间到输出区间的映射并支持双向查询。
// 每个段 [o0,o0+outLen) 对应原文 [a0,a0+origLen)：
// outLen=0 为纯插入段（锚点在 a0，零宽）；origLen=0 为纯删除段（锚点在 o0）。
package span

// Seg 是一段仿射映射：输出位置 o0+k 对应原文 a0+k，k∈[0,min(outLen,origLen))。
// 额外的输出（插入）锚定 a0+origLen；额外的原文（删除）锚定 o0+outLen。
type Seg struct {
	A0, OrigLen int // 原文起点与长度
	O0, OutLen  int // 输出起点与长度
}

// Map 是不可变映射查询视图（用 Build 构造）。
type Map struct {
	segs     []Seg
	origEnd  int
	outEnd   int
	lastLook int // 非导出：最近一次查询检查的区间数
}

// Build 合并相邻可合并的段（同 delta 且可无缝衔接），返回 Map。
func Build(segs []Seg) *Map {
	merged := make([]Seg, 0, len(segs))
	for _, s := range segs {
		if n := len(merged); n > 0 {
			p := merged[n-1]
			if p.OrigLen > 0 && p.OutLen > 0 && s.OrigLen > 0 && s.OutLen > 0 &&
				p.A0+p.OrigLen == s.A0 && p.O0+p.OutLen == s.O0 {
				p.OrigLen += s.OrigLen
				p.OutLen += s.OutLen
				merged[n-1] = p
				continue
			}
		}
		merged = append(merged, s)
	}
	m := &Map{segs: merged}
	if len(merged) > 0 {
		last := merged[len(merged)-1]
		m.origEnd = last.A0 + last.OrigLen
		m.outEnd = last.O0 + last.OutLen
	}
	return m
}

// OrigLen 返回原文总长度。
func (m *Map) OrigLen() int { return m.origEnd }

// OutLen 返回输出总长度。
func (m *Map) OutLen() int { return m.outEnd }

// Segs 返回段数。
func (m *Map) Segs() int { return len(m.segs) }

// LastLookups 返回最近一次 ToOrig/ToOut 检查的区间数。
func (m *Map) LastLookups() int { return m.lastLook }

// findFirst 返回第一个满足 O0+OutLen>o 的段下标，二分并计数。
func (m *Map) findFirst(o int) int {
	lo, hi := 0, len(m.segs)
	for lo < hi {
		m.lastLook++
		mid := (lo + hi) / 2
		if m.segs[mid].O0+m.segs[mid].OutLen > o {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// ToOrig 把输出偏移 o∈[0,OutLen] 映射到原文偏移。
func (m *Map) ToOrig(o int) int {
	if o < 0 || o > m.outEnd {
		panic("span: ToOrig out of range")
	}
	m.lastLook = 0
	if o == m.outEnd {
		return m.origEnd
	}
	idx := m.findFirst(o)
	s := m.segs[idx]
	k := o - s.O0
	if k < s.OrigLen {
		return s.A0 + k
	}
	return s.A0 + s.OrigLen
}

// ToOut 把原文偏移 i∈[0,OrigLen] 映射到输出偏移（被删字节映射到下一存活位置）。
func (m *Map) ToOut(i int) int {
	if i < 0 || i > m.origEnd {
		panic("span: ToOut out of range")
	}
	m.lastLook = 0
	if i == m.origEnd {
		return m.outEnd
	}
	lo, hi := 0, len(m.segs)
	for lo < hi {
		m.lastLook++
		mid := (lo + hi) / 2
		if m.segs[mid].A0+m.segs[mid].OrigLen > i {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	s := m.segs[lo]
	k := i - s.A0
	if k < s.OutLen {
		return s.O0 + k
	}
	return s.O0 + s.OutLen
}
