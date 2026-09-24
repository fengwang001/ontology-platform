// Package span 记录原文区间与输出区间的对应关系，支持双向偏移查询。
//
// 三类区间：identity（orig 与 out 同样长度，一一对应）、
// delete（仅原文，输出中被删）、insert（仅输出，如策略补的末尾换行）。
// insert 区间的原文锚点是"原文终点之后"的虚拟位置（orig 长度），
// 因此对插入字节满足 ToOut(ToOrig(o))==o；这是输出比原文长的唯一情形。
package span

type kind uint8

const (
	kIdent kind = iota
	kDelete
	kInsert
)

type seg struct {
	kind                       kind
	origStart, origEnd         int
	outStart, outEnd           int
}

// Builder 增量构建映射。零值即可使用。
type Builder struct {
	segs                            []seg
	origPos, outPos                 int
}

// Identity 登记 n 个一一对应的字节。
func (b *Builder) Identity(n int) {
	if n <= 0 {
		return
	}
	if m := len(b.segs); m > 0 && b.segs[m-1].kind == kIdent &&
		b.segs[m-1].origEnd == b.origPos && b.segs[m-1].outEnd == b.outPos {
		b.segs[m-1].origEnd += n
		b.segs[m-1].outEnd += n
	} else {
		b.segs = append(b.segs, seg{kIdent, b.origPos, b.origPos + n, b.outPos, b.outPos + n})
	}
	b.origPos += n
	b.outPos += n
}

// Delete 登记 n 个被删原文字节。
func (b *Builder) Delete(n int) {
	if n <= 0 {
		return
	}
	if m := len(b.segs); m > 0 && b.segs[m-1].kind == kDelete && b.segs[m-1].origEnd == b.origPos {
		b.segs[m-1].origEnd += n
	} else {
		b.segs = append(b.segs, seg{kDelete, b.origPos, b.origPos + n, b.outPos, b.outPos})
	}
	b.origPos += n
}

// Insert 登记 n 个无原文对应的输出字节（锚点为当前原文终点）。
func (b *Builder) Insert(n int) {
	if n <= 0 {
		return
	}
	b.segs = append(b.segs, seg{kInsert, b.origPos, b.origPos, b.outPos, b.outPos + n})
	b.outPos += n
}

// TrimTail 把输出末尾至多 n 个 identity 字节改写为删除，并返回实际改写数。
// insert 区间（策略补的换行）直接弹出。
func (b *Builder) TrimTail(n int) int {
	removed := 0
	for n > 0 && len(b.segs) > 0 {
		m := len(b.segs) - 1
		s := b.segs[m]
		if s.kind == kInsert {
			b.segs = b.segs[:m]
			b.outPos -= s.outEnd - s.outStart
			continue
		}
		if s.kind != kIdent {
			break
		}
		cut := s.origEnd - s.origStart
		if cut > n {
			cut = n
		}
		b.segs[m].origEnd -= cut
		b.segs[m].outEnd -= cut
		b.outPos -= cut
		removed += cut
		n -= cut
		if b.segs[m].origStart == b.segs[m].origEnd {
			b.segs = b.segs[:m]
		}
	}
	return removed
}

// Build 冻结为可查询映射。
func (b *Builder) Build() *Map {
	m := &Map{segs: make([]seg, len(b.segs)), origLen: b.origPos, outLen: b.outPos}
	copy(m.segs, b.segs)
	return m
}

// Map 是只读双向偏移映射。
type Map struct {
	segs                              []seg
	origLen, outLen                   int
	lastChecks                        int
}

// LastChecks 返回最近一次查询二分检查的区间数（非导出语义，供测试断言）。
func (m *Map) LastChecks() int { return m.lastChecks }

// OrigLen 与 OutLen 返回两端长度。
func (m *Map) OrigLen() int { return m.origLen }
func (m *Map) OutLen() int  { return m.outLen }

// Segments 返回映射区间数。
func (m *Map) Segments() int { return len(m.segs) }

// ToOrig 把输出偏移 o ∈ [0,OutLen] 映射回原文偏移（插入字节映射到原文终点）。
func (m *Map) ToOrig(o int) int {
	i := sortSearch(len(m.segs), func(i int) bool { return o < m.segs[i].outStart }) - 1
	m.lastChecks = bitLen(len(m.segs))
	if i < 0 {
		return 0
	}
	s := m.segs[i]
	if s.kind == kIdent && o < s.outEnd {
		return s.origStart + (o - s.outStart)
	}
	if s.kind == kInsert && o <= s.outEnd {
		return m.origLen
	}
	if s.kind == kDelete {
		return s.origStart
	}
	return s.origEnd
}

// ToOut 把原文偏移 i ∈ [0,OrigLen] 映射到输出偏移。
// 被删字节右靠到紧随删除区间的输出位置（即换行之后/下一个保留点）。
func (m *Map) ToOut(i int) int {
	j := sortSearch(len(m.segs), func(j int) bool { return i < m.segs[j].origStart }) - 1
	m.lastChecks = bitLen(len(m.segs))
	if j < 0 {
		return 0
	}
	s := m.segs[j]
	if s.kind == kIdent && i < s.origEnd {
		return s.outStart + (i - s.origStart)
	}
	if s.kind == kInsert {
		return s.outStart
	}
	if j+1 < len(m.segs) {
		return m.segs[j+1].outStart
	}
	return m.outLen
}

// Shift 返回平移 orig/out 坐标后的映射副本（供 par 拼接）。
func (m *Map) Shift(origBase, outBase int) *Map {
	cp := &Map{origLen: m.origLen + origBase, outLen: m.outLen + outBase, segs: make([]seg, len(m.segs))}
	for i, s := range m.segs {
		s.origStart += origBase
		s.origEnd += origBase
		s.outStart += outBase
		s.outEnd += outBase
		cp.segs[i] = s
	}
	return cp
}

// Clip 裁剪到原文区间 [lo,hi)，坐标归零；返回裁剪后映射以及对应的输出字节窗口。
// keepInsert 为 true 时保留 hi 之后的 insert 区间（供最后一段承接策略补的换行）。
func (m *Map) Clip(lo, hi int, keepInsert bool) (*Map, int, int) {
	outLo, outHi := m.ToOut(lo), m.ToOut(hi)
	m.lastChecks = 0
	var out []seg
	for _, s := range m.segs {
		if s.kind == kInsert {
			continue
		}
		if s.origEnd <= lo || s.origStart >= hi {
			continue
		}
		c := s
		if c.origStart < lo {
			d := lo - c.origStart
			c.origStart = lo
			if c.kind == kIdent {
				c.outStart += d
			}
		}
		if c.origEnd > hi {
			d := c.origEnd - hi
			c.origEnd = hi
			if c.kind == kIdent {
				c.outEnd -= d
			}
		}
		c.origStart -= lo
		c.origEnd -= lo
		if c.kind == kDelete {
			c.outStart, c.outEnd = outHi-outLo, outHi-outLo
		} else {
			c.outStart -= outLo
			c.outEnd -= outLo
		}
		out = append(out, c)
	}
	if keepInsert {
		for _, s := range m.segs {
			if s.kind != kInsert {
				continue
			}
			out = append(out, seg{kInsert, hi - lo, hi - lo,
				outHi - outLo, s.outEnd - outLo})
			outHi = s.outEnd
		}
	}
	return &Map{segs: out, origLen: hi - lo, outLen: outHi - outLo}, outLo, outHi
}

// Concat 按顺序拼接已平移好坐标的多个映射。
func Concat(parts ...*Map) *Map {
	var segs []seg
	var ol, nl int
	for _, p := range parts {
		segs = append(segs, p.segs...)
		if len(p.segs) > 0 {
			q := p.segs[len(p.segs)-1]
			ol, nl = q.origEnd, q.outEnd
		}
	}
	return &Map{segs: segs, origLen: ol, outLen: nl}
}

func sortSearch(n int, f func(int) bool) int {
	i, j := 0, n
	for i < j {
		h := int(uint(i+j) >> 1)
		if !f(h) {
			i = h + 1
		} else {
			j = h
		}
	}
	return i
}

func bitLen(n int) int {
	b := 0
	for n > 0 {
		n >>= 1
		b++
	}
	if b == 0 {
		b = 1
	}
	return b
}
