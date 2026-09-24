// Package span 记录原文区间与输出区间的对应关系，支持双向偏移查询。
//
// 映射由一串首尾相接的段构成：等长段表示原文与输出逐字节对应；
// 纯删除段表示原文字节被删除（输出长度 0），其 ToOut 坍缩到下一个存活字节
// 的输出偏移（见 DESIGN.md 第 2 节）。
package span

// Seg 是一个映射段：原文 [OStart,OStart+OLen) 对应输出 [UStart,UStart+ULen)。
// ULen==0 表示纯删除段；非删除段必有 OLen==ULen。
type Seg struct {
	OStart int
	UStart int
	OLen   int
	ULen   int
}

func (s Seg) oEnd() int { return s.OStart + s.OLen }
func (s Seg) uEnd() int { return s.UStart + s.ULen }

// Map 是不可变映射（由 Builder 构造）。
type Map struct {
	segs []Seg
	ol   int // 原文长度
	ul   int // 输出长度
	// probe 记录最近一次查询检查的区间数。
	probe int
}

// ProbeCount 返回最近一次 ToOrig/ToOut 查询检查的区间数。
func (m *Map) ProbeCount() int { return m.probe }

// Segs 暴露底层段（供 par 平移拼接），返回内部切片，调用方不得修改。
func (m *Map) Segs() []Seg { return m.segs }

// OrigLen 返回原文长度。
func (m *Map) OrigLen() int { return m.ol }

// OutLen 返回输出长度。
func (m *Map) OutLen() int { return m.ul }

// ToOrig 把输出偏移 o（范围 [0,OutLen]）映射为原文偏移，单调不减。
func (m *Map) ToOrig(o int) int {
	n := len(m.segs)
	lo, hi := 0, n
	for lo < hi {
		m.probe++
		mid := (lo + hi) / 2
		if m.segs[mid].uEnd() <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == n {
		return m.ol // 终点
	}
	// lo 是第一个 uEnd>o 的段；删除段 ULen==0 不覆盖任何偏移，必已被越过，
	// 因此 lo 必为存活段。
	s := m.segs[lo]
	return s.OStart + (o - s.UStart)
}

// ToOut 把原文偏移 i（范围 [0,OrigLen]）映射为输出偏移，单调不减。
func (m *Map) ToOut(i int) int {
	n := len(m.segs)
	lo, hi := 0, n
	for lo < hi {
		m.probe++
		mid := (lo + hi) / 2
		if m.segs[mid].oEnd() <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	k := lo
	if k < n {
		s := m.segs[k]
		if s.ULen > 0 {
			return s.UStart + (i - s.OStart)
		}
		// 删除段内部（或其边界）：坍缩到输出侧同一点。
		return s.UStart
	}
	return m.ul // 终点
}

// Builder 增量构造 Map，零值可用。
type Builder struct {
	segs []Seg
	ol   int
	ul   int
}

// Ident 登记 [orig,orig+n) 与 [out,out+n) 的逐字节对应，相邻等长段自动合并。
func (b *Builder) Ident(orig, out, n int) {
	if n <= 0 {
		return
	}
	if k := len(b.segs) - 1; k >= 0 {
		s := &b.segs[k]
		if s.OLen == s.ULen && s.oEnd() == orig && s.uEnd() == out {
			s.OLen += n
			s.ULen += n
			b.ol += n
			b.ul += n
			return
		}
	}
	b.segs = append(b.segs, Seg{OStart: orig, UStart: out, OLen: n, ULen: n})
	b.ol += n
	b.ul += n
}

// Delete 登记被删原文字节区间 [orig,orig+n)（不产生输出）。
func (b *Builder) Delete(orig, n int) {
	if n <= 0 {
		return
	}
	b.segs = append(b.segs, Seg{OStart: orig, UStart: b.ul, OLen: n})
	b.ol += n
}

// Build 生成不可变 Map。
func (b *Builder) Build() *Map {
	return &Map{segs: b.segs, ol: b.ol, ul: b.ul}
}
