// Package span 维护原文区间与输出区间的对应关系，支持双向偏移查询。
// 映射由一串 run 组成：identity（orig 与 out 同步推进）、delete
//（仅 orig 推进）、insert（仅 out 推进）。相邻同类 run 自动合并，
// 因此无删除的文本只有常数个 run。查询一律二分。
package span

import "sort"

// Run 表示一个映射区间，坐标为累计的原文/输出起点。
type Run struct {
	OrigOff int // 原文起点
	OutOff  int // 输出起点
	OrigLen int // 原文长度（0 表示纯插入）
	OutLen  int // 输出长度（0 表示删除）
}

// Builder 增量构建 run 序列，按原文顺序调用。
type Builder struct{ runs []Run }

func (b *Builder) last() *Run { return &b.runs[len(b.runs)-1] }

// Keep 登记 n 个原样保留的字节。
func (b *Builder) Keep(n int) {
	if n <= 0 {
		return
	}
	if len(b.runs) > 0 {
		if r := b.last(); r.OrigLen > 0 && r.OutLen == r.OrigLen &&
			r.OrigOff+r.OrigLen == b.OrigLen() {
			r.OrigLen += n
			r.OutLen += n
			return
		}
	}
	b.runs = append(b.runs, Run{OrigOff: b.OrigLen(), OutOff: b.OutLen(), OrigLen: n, OutLen: n})
}

// Drop 登记 n 个被删除的原文字节（相邻删除自动合并）。
func (b *Builder) Drop(n int) {
	if n <= 0 {
		return
	}
	if len(b.runs) > 0 {
		if r := b.last(); r.OutLen == 0 && r.OrigOff+r.OrigLen == b.OrigLen() {
			r.OrigLen += n
			return
		}
	}
	b.runs = append(b.runs, Run{OrigOff: b.OrigLen(), OutOff: b.OutLen(), OrigLen: n})
}

// Insert 登记 n 个无原文对应的输出字节（如末尾补的换行）。
func (b *Builder) Insert(n int) {
	if n <= 0 {
		return
	}
	b.runs = append(b.runs, Run{OrigOff: b.OrigLen(), OutOff: b.OutLen(), OutLen: n})
}

// Runs 返回内部 run 切片（拼接修正时直接改写）。
func (b *Builder) Runs() []Run { return b.runs }

// OrigLen 为已登记原文总字节数。
func (b *Builder) OrigLen() int {
	if len(b.runs) == 0 {
		return 0
	}
	r := b.last()
	return r.OrigOff + r.OrigLen
}

// OutLen 为已登记输出总字节数。
func (b *Builder) OutLen() int {
	if len(b.runs) == 0 {
		return 0
	}
	r := b.last()
	return r.OutOff + r.OutLen
}

// Mapper 对冻结的 run 序列提供双向查询。
type Mapper struct {
	runs     []Run
	origEnd  int
	outEnd   int
	lastScan int // 最近一次查询检查的区间数
}

// Mapper 冻结当前映射。
func (b *Builder) Mapper() *Mapper {
	return &Mapper{runs: b.runs, origEnd: b.OrigLen(), outEnd: b.OutLen()}
}

// LastScan 返回最近一次 ToOrig/ToOut 二分检查的区间数。
func (m *Mapper) LastScan() int { return m.lastScan }

// ToOrig 把输出偏移 o ∈ [0, OutLen] 映射为原文偏移。
func (m *Mapper) ToOrig(o int) int {
	i := sort.Search(len(m.runs), func(i int) bool {
		r := m.runs[i]
		return r.OutOff+r.OutLen > o || (r.OutLen == 0 && r.OutOff == o)
	})
	m.lastScan = log2(len(m.runs)) + 1
	if i == len(m.runs) {
		return m.origEnd
	}
	r := m.runs[i]
	switch {
	case r.OutLen == 0: // 删除 run
		return r.OrigOff
	case r.OrigLen == 0: // 插入 run：钳制到插入点
		return r.OrigOff
	default:
		return r.OrigOff + (o - r.OutOff)
	}
}

// ToOut 把原文偏移 i ∈ [0, OrigLen] 映射为输出偏移。
func (m *Mapper) ToOut(i int) int {
	j := sort.Search(len(m.runs), func(j int) bool {
		r := m.runs[j]
		return r.OrigOff+r.OrigLen > i || (r.OrigLen == 0 && r.OrigOff == i)
	})
	m.lastScan = log2(len(m.runs)) + 1
	if j == len(m.runs) {
		return m.outEnd
	}
	r := m.runs[j]
	switch {
	case r.OrigLen == 0: // 插入 run
		return r.OutOff
	case r.OutLen == 0: // 删除 run
		return r.OutOff
	default:
		return r.OutOff + (i - r.OrigOff)
	}
}

// OrigLen 返回原文总长度。
func (m *Mapper) OrigLen() int { return m.origEnd }

// OutLen 返回输出总长度。
func (m *Mapper) OutLen() int { return m.outEnd }

func log2(n int) int {
	k := 0
	for n > 1 {
		n >>= 1
		k++
	}
	return k
}
