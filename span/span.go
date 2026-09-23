// Package span 记录原文区间与输出区间的双向偏移映射，仅在坍缩点分段。
package span

// seg 描述一段输出区间 [out0,out1) 与原文坐标的关系。
// 普通保留段 orig1-orig0 == out1-out0；
// 插入段（规范生成的 \n）为零宽 orig0==orig1==插入点；
// 被删字节是相邻两段之间的输出零宽缝隙，其 ToOut 取缝隙右端。
type seg struct {
	out0, out1   int
	orig0, orig1 int
}

func contiguous(a, b seg) bool {
	return a.out1 == b.out0 && a.orig1 == b.orig0
}

// Builder 以输出顺序追加映射段；相邻且双侧连续的段自动合并，
// 因此无删除内容时整张表只有常数个区间。
type Builder struct {
	segs  []seg
	outN  int
	origN int
}

// Retain 登记 n 个原样保留的字节。
func (b *Builder) Retain(n int) {
	if n <= 0 {
		return
	}
	s := seg{b.outN, b.outN + n, b.origN, b.origN + n}
	if k := len(b.segs); k > 0 && contiguous(b.segs[k-1], s) {
		b.segs[k-1].out1 = s.out1
		b.segs[k-1].orig1 = s.orig1
	} else {
		b.segs = append(b.segs, s)
	}
	b.outN += n
	b.origN += n
}

// Delete 登记 n 个被删原文字节（输出零宽坍缩）。
func (b *Builder) Delete(n int) { b.origN += n }

// Insert 登记 n 个新生成的输出字节，原文坐标固定为当前插入点。
func (b *Builder) Insert(n int) {
	if n <= 0 {
		return
	}
	b.segs = append(b.segs, seg{b.outN, b.outN + n, b.origN, b.origN})
	b.outN += n
}

// Truncate 截断映射到输出长度 at（仅在段边界使用）。
func (b *Builder) Truncate(at int) {
	for len(b.segs) > 0 && b.segs[len(b.segs)-1].out0 >= at {
		b.segs = b.segs[:len(b.segs)-1]
	}
	if len(b.segs) > 0 && b.segs[len(b.segs)-1].out1 > at {
		b.segs[len(b.segs)-1].out1 = at
	}
	b.outN = at
}

// Build 冻结为只读映射。
func (b *Builder) Build() *Map {
	cp := make([]seg, len(b.segs))
	copy(cp, b.segs)
	return &Map{segs: cp, outN: b.outN, origN: b.origN}
}

// Map 是不可变双向偏移映射，定义域为 [0,len) 外加终点 len。
type Map struct {
	segs  []seg
	outN  int
	origN int
	probe int
}

// byOut 二分首个 out1 > o 的段，probe 记录检查次数。
func (m *Map) byOut(o int) int {
	m.probe = 0
	lo, hi := 0, len(m.segs)
	for lo < hi {
		m.probe++
		mid := int(uint(lo+hi) >> 1)
		if m.segs[mid].out1 <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// ToOrig 输出偏移 → 原文偏移，单调不减。
func (m *Map) ToOrig(o int) int {
	m.probe = 0
	if o <= 0 {
		return 0
	}
	if o >= m.outN {
		return m.origN
	}
	i := m.byOut(o)
	s := m.segs[i]
	if s.orig0 == s.orig1 {
		return s.orig0 // 插入点（规范化生成的字节）
	}
	return s.orig0 + (o - s.out0)
}

// ToOut 原文偏移 → 输出偏移，单调不减；被删字节映射到坍缩右端（行尾 \n 或终点）。
func (m *Map) ToOut(i int) int {
	m.probe = 0
	if i <= 0 {
		return 0
	}
	if i >= m.origN {
		return m.outN
	}
	lo, hi := 0, len(m.segs)
	for lo < hi {
		m.probe++
		mid := int(uint(lo+hi) >> 1)
		if m.segs[mid].orig1 <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == len(m.segs) {
		return m.outN
	}
	s := m.segs[lo]
	if i < s.orig0 || s.orig0 == s.orig1 {
		return s.out0 // 落在删去缝隙或插入点
	}
	return s.out0 + (i - s.orig0)
}

// OutLen 输出总长度。
func (m *Map) OutLen() int { return m.outN }

// OrigLen 原文总长度。
func (m *Map) OrigLen() int { return m.origN }

// OrigLen 返回 Builder 当前已登记的原文长度。
func (b *Builder) OrigLen() int { return b.origN }

// Segments 返回映射区间数（只随坍缩点增长）。
func (m *Map) Segments() int { return len(m.segs) }

// LastProbe 返回最近一次查询二分检查的区间数。
func (m *Map) LastProbe() int { return m.probe }
