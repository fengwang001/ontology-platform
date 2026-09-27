// Package span 记录原文区间与输出区间的对应关系，支持双向偏移查询。
//
// 规范化被建模为逐字节“保留或删除”：保留区间 1 原字节↔1 输出字节；
// 删除区间占原文长度、不占输出长度。末尾策略额外追加的 \n 是无原文支撑的
// 合成区间（origStart=origEnd），只在输出终点处出现。
package span

// Run 描述一段连续、同长度比的映射区间，区间均为左闭右开。
type Run struct {
	OrigStart int // 原文起点
	OrigEnd   int // 原文终点（合成追加时 OrigStart==OrigEnd）
	OutStart  int // 输出起点
	OutEnd    int // 输出终点（删除时 OutStart==OutEnd）
	Synthetic bool // 无原文字节支撑（末尾策略追加的 \n）
}

// Kept 报告该区间是否为逐字节保留。
func (r Run) Kept() bool {
	return r.OrigEnd-r.OrigStart == r.OutEnd-r.OutStart && r.OrigEnd > r.OrigStart
}

// Builder 以追加方式构造区间表，自动合并相邻同类区间。
type Builder struct {
	runs []Run
}

// NewBuilder 返回空构造器。
func NewBuilder() *Builder { return &Builder{} }

// Add 追加一个区间；与末区间相邻且同为保留/同为非合成删除时自动合并。
func (b *Builder) Add(r Run) {
	if n := len(b.runs); n > 0 {
		last := &b.runs[n-1]
		if last.OrigEnd == r.OrigStart && last.OutEnd == r.OutStart &&
			!last.Synthetic && !r.Synthetic && last.Kept() == r.Kept() {
			last.OrigEnd = r.OrigEnd
			last.OutEnd = r.OutEnd
			return
		}
	}
	b.runs = append(b.runs, r)
}

// Build 产出不可变映射表。
func (b *Builder) Build() *Map {
	m := &Map{runs: make([]Run, len(b.runs))}
	copy(m.runs, b.runs)
	return m
}

// Map 是不可变双向偏移映射。
type Map struct {
	runs   []Run
	checks int // 最近一次查询检查的区间数（非导出计数器）
}

// Segments 返回映射区间总数。
func (m *Map) Segments() int { return len(m.runs) }

// Checks 返回最近一次 ToOrig/ToOut 二分检查的区间数。
func (m *Map) Checks() int { return m.checks }

// ToOrig 把输出偏移 o∈[0,输出终点] 映射回原文偏移，单调不减。
func (m *Map) ToOrig(o int) int {
	lo, hi, c := 0, len(m.runs), 0
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		c++
		if m.runs[mid].OutEnd <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.checks = c
	if lo >= len(m.runs) {
		return m.runs[len(m.runs)-1].OrigEnd
	}
	r := m.runs[lo]
	if o < r.OutStart {
		o = r.OutStart
	}
	if r.Synthetic {
		return r.OrigStart
	}
	return r.OrigStart + (o - r.OutStart)
}

// ToOut 把原文偏移 i∈[0,原文终点] 映射到输出偏移，单调不减。
func (m *Map) ToOut(i int) int {
	lo, hi, c := 0, len(m.runs), 0
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		c++
		if m.runs[mid].OrigEnd <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.checks = c
	if lo >= len(m.runs) {
		return m.runs[len(m.runs)-1].OutEnd
	}
	r := m.runs[lo]
	if i < r.OrigStart {
		i = r.OrigStart
	}
	if r.Synthetic || !r.Kept() {
		return r.OutStart
	}
	return r.OutStart + (i - r.OrigStart)
}
