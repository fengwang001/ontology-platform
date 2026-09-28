// Package span 记录原文区间与输出区间的双向偏移映射。
// 只存被保留的段；段与段之间（以及首尾）的间隙即被删除的字节。
package span

// Seg 是一段逐字节保留的区间：原文 [Orig, Orig+Len) 对应输出 [Out, Out+Len)。
type Seg struct {
	Orig int
	Out  int
	Len  int
}

// Map 是不可变映射（由 Builder 构造）。seg 按 Orig、Out 严格有序且不重叠。
type Map struct {
	seg []Seg
	ol  int // 原文总长
	pl  int // 输出总长
	// lastProbe 记录最近一次 ToOrig/ToOut 检查过的区间数。
	lastProbe int
}

// LastProbe 返回最近一次查询二分检查的区间数。
func (m *Map) LastProbe() int { return m.lastProbe }

// LenOrig 与 LenOut 返回两侧总长。
func (m *Map) LenOrig() int { return m.ol }
func (m *Map) LenOut() int  { return m.pl }

// Segments 返回保留段副本，区间数应只随删除点数增长。
func (m *Map) Segments() []Seg {
	out := make([]Seg, len(m.seg))
	copy(out, m.seg)
	return out
}

func ceilLog2(n int) int {
	k := 0
	for 1<<k < n {
		k++
	}
	return k
}

// ToOrig 把输出偏移 o（0..LenOut）映回原文偏移，单调不减。
func (m *Map) ToOrig(o int) int {
	lo, hi, probe := 0, len(m.seg), 0
	for lo < hi {
		probe++
		mid := lo + (hi-lo)/2
		if o < m.seg[mid].Out {
			hi = mid
		} else if o >= m.seg[mid].Out+m.seg[mid].Len {
			lo = mid + 1
		} else {
			m.lastProbe = probe
			return m.seg[mid].Orig + (o - m.seg[mid].Out)
		}
	}
	m.lastProbe = probe
	if lo == 0 {
		if len(m.seg) > 0 {
			return m.seg[0].Orig
		}
		return m.ol
	}
	s := m.seg[lo-1]
	return s.Orig + s.Len // 落在输出删除间隙（Trim 收缩输出尾部）
}

// ToOut 把原文偏移 i（0..LenOrig）映到输出偏移，单调不减。
// 被删字节映到其后第一个保留段起点；EOF 处删除映到输出终点。
func (m *Map) ToOut(i int) int {
	lo, hi, probe := 0, len(m.seg), 0
	for lo < hi {
		probe++
		mid := lo + (hi-lo)/2
		if i < m.seg[mid].Orig {
			hi = mid
		} else if i >= m.seg[mid].Orig+m.seg[mid].Len {
			lo = mid + 1
		} else {
			m.lastProbe = probe
			return m.seg[mid].Out + (i - m.seg[mid].Orig)
		}
	}
	m.lastProbe = probe
	if lo < len(m.seg) {
		return m.seg[lo].Out
	}
	return m.pl
}

// Builder 顺序构造 Map。
type Builder struct {
	seg    []Seg
	ol, pl int
}

// Keep 登记原文 orig 起、长度 n 的字节被逐字节保留，输出位置自动顺延。
func (b *Builder) Keep(orig, n int) {
	if n <= 0 {
		return
	}
	if k := len(b.seg); k > 0 {
		last := &b.seg[k-1]
		if last.Orig+last.Len == orig && last.Out+last.Len == b.pl {
			last.Len += n
			b.ol = orig + n
			b.pl += n
			return
		}
	}
	b.seg = append(b.seg, Seg{Orig: orig, Out: b.pl, Len: n})
	b.ol = orig + n
	b.pl += n
}

// NoteOrig 标记已消费但不保留的原文偏移（删除间隙）。
func (b *Builder) NoteOrig(i int) {
	if i > b.ol {
		b.ol = i
	}
}

// TrimOut 把输出尾部裁到 outLen：超出的保留段被裁掉或缩短，
// 原文长度保持不变（这些字节成为末尾删除间隙）。
func (b *Builder) TrimOut(outLen int) {
	for len(b.seg) > 0 {
		s := &b.seg[len(b.seg)-1]
		if s.Out >= outLen {
			b.seg = b.seg[:len(b.seg)-1]
			continue
		}
		if s.Out+s.Len > outLen {
			s.Len = outLen - s.Out
		}
		break
	}
	b.pl = outLen
}

// Build 固化映射。
func (b *Builder) Build() *Map {
	return &Map{seg: b.seg, ol: b.ol, pl: b.pl}
}

// ProbeBound 是 LastProbe 的理论上界：2*ceil(log2(区间数))+4。
func ProbeBound(segs int) int { return 2*ceilLog2(segs) + 4 }
// Segs 返回当前保留段（直接切片，供组装阶段读取）。
func (b *Builder) Segs() []Seg { return b.seg }

// Build 固化映射。
