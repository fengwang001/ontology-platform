// Package span 记录原文区间与输出区间的对应关系并支持双向查询。
// 区间为左闭右开；相邻同类区间合并，因此区间数只随"删除/插入点"增长。
package span

// Run 是一个映射区间：原文 [op,op+ol) ↔ 输出 [np,np+nl)。
// ol>0,nl>0 为保留；ol>0,nl==0 为删除；ol==0,nl>0 为插入。
type Run struct{ OP, OL, NP, NL int }

// Map 是追加式区间表，非并发安全。
type Map struct {
	runs      []Run
	lastCheck int // 最近一次查询二分检查的区间数
}

// New 创建空映射。
func New() *Map { return &Map{} }

func (m *Map) Keep(ol, nl int) {
	if ol <= 0 && nl <= 0 {
		return
	}
	op, np := m.origLen(), m.outLen()
	if r := len(m.runs); r > 0 && m.runs[r-1].OL > 0 && m.runs[r-1].NL > 0 {
		m.runs[r-1].OL += ol
		m.runs[r-1].NL += nl
		return
	}
	m.runs = append(m.runs, Run{OP: op, OL: ol, NP: np, NL: nl})
}

func (m *Map) Delete(ol int) {
	if ol <= 0 {
		return
	}
	op := m.origLen()
	if r := len(m.runs); r > 0 && m.runs[r-1].NL == 0 {
		m.runs[r-1].OL += ol
		return
	}
	m.runs = append(m.runs, Run{OP: op, OL: ol})
}

func (m *Map) Insert(nl int) {
	if nl <= 0 {
		return
	}
	np := m.outLen()
	if r := len(m.runs); r > 0 && m.runs[r-1].OL == 0 {
		m.runs[r-1].NL += nl
		return
	}
	m.runs = append(m.runs, Run{NP: np, NL: nl})
}

func (m *Map) origLen() int {
	if len(m.runs) == 0 {
		return 0
	}
	r := m.runs[len(m.runs)-1]
	return r.OP + r.OL
}

func (m *Map) outLen() int {
	if len(m.runs) == 0 {
		return 0
	}
	r := m.runs[len(m.runs)-1]
	return r.NP + r.NL
}

// OrigLen 与 OutLen 返回两端总长度（均含终点）。
func (m *Map) OrigLen() int { return m.origLen() }
func (m *Map) OutLen() int  { return m.outLen() }

// Runs 导出区间快照（供 par 平移重建）。
func (m *Map) Runs() []Run {
	out := make([]Run, len(m.runs))
	copy(out, m.runs)
	return out
}

// FromRuns 直接由区间序列重建映射（坐标已平移，按起点排序）。
func FromRuns(rs []Run) *Map {
	m := New()
	for _, r := range rs {
		switch {
		case r.OL > 0 && r.NL > 0:
			m.runs = append(m.runs, r)
		case r.OL > 0:
			m.runs = append(m.runs, r)
		case r.NL > 0:
			m.runs = append(m.runs, r)
		}
	}
	return m
}

// LastCheck 返回最近一次 ToOrig/ToOut 查询二分检查的区间数。
func (m *Map) LastCheck() int { return m.lastCheck }

// ToOrig 把输出偏移 o（0..OutLen）映射为原文偏移。
func (m *Map) ToOrig(o int) int {
	return m.at(o, true)
}

// ToOut 把原文偏移 i（0..OrigLen）映射为输出偏移。
func (m *Map) ToOut(i int) int {
	return m.at(i, false)
}

func (m *Map) at(pos int, out bool) int {
	lo, hi := 0, len(m.runs)
	m.lastCheck = 0
	for lo < hi {
		m.lastCheck++
		mid := (lo + hi) / 2
		r := m.runs[mid]
		var start, length int
		if out {
			start, length = r.NP, r.NL
		} else {
			start, length = r.OP, r.OL
		}
		switch {
		case pos < start:
			hi = mid
		case start+length <= pos:
			lo = mid + 1
		default:
			if out {
				return r.OP + (pos - r.NP)
			}
			return r.NP + (pos - r.OP)
		}
	}
	if out {
		if lo < len(m.runs) {
			return m.runs[lo].OP
		}
		return m.origLen()
	}
	if lo < len(m.runs) {
		return m.runs[lo].NP
	}
	return m.outLen()
}
