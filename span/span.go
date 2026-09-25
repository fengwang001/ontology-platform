// Package span 记录原文区间与输出区间的双向偏移映射。
// 映射只在「删除点」处产生分段；无删除的长文本只对应常数个区间。
package span

// entry 是一段「逐字节保留」的对应：原文 [i0,i0+ln) ↔ 输出 [o0,o0+ln)。
// ln 可为 0（空输入/纯插入锚点）。删除段不出现在 entry 中，
// 查询时按「贴向删除间隙右侧最近保留位」折叠。
type entry struct {
	i0 int
	o0 int
	ln int
}

// Map 是不可变双向映射，由 Builder 构造。
type Map struct {
	e       []entry
	origLen int
	outLen  int
	checked int // 最近一次查询检查的区间数（非导出计数器）
}

// Checked 返回最近一次 ToOrig/ToOut 检查的映射区间数。
func (m *Map) Checked() int { return m.checked }

// OrigLen 返回原文总长度。
func (m *Map) OrigLen() int { return m.origLen }

// OutLen 返回输出总长度。
func (m *Map) OutLen() int { return m.outLen }

func (m *Map) probe(pred func(int) bool) int {
	lo, hi := 0, len(m.e)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		m.checked++
		if pred(mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// ToOrig 把输出偏移 o（[0,OutLen]）映射回原文偏移，单调不减。
func (m *Map) ToOrig(o int) int {
	m.checked = 0
	// 最后一个 o0 <= o 的区间
	j := m.probe(func(k int) bool { return m.e[k].o0 > o }) - 1
	m.checked++
	if j >= 0 && o < m.e[j].o0+m.e[j].ln {
		return m.e[j].i0 + (o - m.e[j].o0)
	}
	m.checked++
	if j+1 < len(m.e) {
		return m.e[j+1].i0 // 删除间隙：折叠到右侧保留位
	}
	return m.origLen
}

// ToOut 把原文偏移 i（[0,OrigLen]）映射到输出偏移，单调不减。
func (m *Map) ToOut(i int) int {
	m.checked = 0
	j := m.probe(func(k int) bool { return m.e[k].i0 > i }) - 1
	m.checked++
	if j >= 0 && i < m.e[j].i0+m.e[j].ln {
		return m.e[j].o0 + (i - m.e[j].i0)
	}
	m.checked++
	if j+1 < len(m.e) {
		return m.e[j+1].o0
	}
	return m.outLen
}

// Builder 按流式处理顺序累积保留/删除段。
type Builder struct {
	e  []entry
	ii int // 已消费原文长度
	oi int // 已产生输出长度
}

// NewBuilder 创建空映射构造器。
func NewBuilder() *Builder { return &Builder{} }

// Keep 记录 n 个原文与输出逐字节对齐的保留字节。
func (b *Builder) Keep(n int) {
	if n <= 0 {
		return
	}
	if k := len(b.e) - 1; k >= 0 && b.e[k].i0+b.e[k].ln == b.ii && b.e[k].o0+b.e[k].ln == b.oi {
		b.e[k].ln += n
	} else {
		b.e = append(b.e, entry{i0: b.ii, o0: b.oi, ln: n})
	}
	b.ii += n
	b.oi += n
}

// Delete 记录 n 个被删除的原文字节（行尾空白或 \r\n 中的 \r）。
func (b *Builder) Delete(n int) {
	if n > 0 {
		b.ii += n
	}
}

// Insert 在当前原文位置记录一个无原文来源的输出字节（如空输入补的 \n）。
func (b *Builder) Insert() {
	b.e = append(b.e, entry{i0: b.ii, o0: b.oi, ln: 1})
	b.oi++
}

// TrimOutput 从输出尾撤销 n 个已保留字节（末尾换行折叠），
// 对应原文字节变为删除间隙，映射到新的输出终点。
func (b *Builder) TrimOutput(n int) {
	for n > 0 && len(b.e) > 0 {
		k := len(b.e) - 1
		t := b.e[k].ln
		if t > n {
			b.e[k].ln -= n
			b.oi -= n
			return
		}
		b.e = b.e[:k]
		b.oi -= t
		n -= t
	}
}

// Build 冻结为不可变 Map。
func (b *Builder) Build() *Map {
	e := make([]entry, len(b.e), len(b.e)+1)
	copy(e, b.e)
	if len(e) == 0 {
		e = append(e, entry{}) // 空输入锚点
	}
	return &Map{e: e, origLen: b.ii, outLen: b.oi, checked: 0}
}
