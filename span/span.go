// Package span 维护原文区间与输出区间的对应关系，支持双向偏移查询。
// 区间数只随删除点个数增长，不随输出字节数增长（相邻同型区间自动合并）。
package span

// Run 把原文区间 [Orig, Orig+OLen) 映射到输出区间 [Out, Out+ULen)。
// OLen>0,ULen=0 表示被删除的原文；OLen=0,ULen>0 表示合成的输出字节。
type Run struct {
	Orig, Out, OLen, ULen int
}

// Map 是按原文/输出双坐标有序的不重叠区间表。
type Map struct {
	runs  []Run
	orig  int // 原文总长
	out   int // 输出总长
	probe int // 最近一次查询检查的区间数
}

// Add 追加一个区间；与末尾区间相邻且同型（同为 1:1 或同为纯删除）时合并。
func (m *Map) Add(orig, out, olen, ulen int) {
	if olen == 0 && ulen == 0 {
		return
	}
	if n := len(m.runs); n > 0 {
		l := &m.runs[n-1]
		contig := l.Orig+l.OLen == orig && l.Out+l.ULen == out
		same := (l.OLen == l.ULen && olen == ulen) || (l.ULen == 0 && ulen == 0)
		if contig && same {
			l.OLen += olen
			l.ULen += ulen
			m.orig, m.out = orig+olen, out+ulen
			return
		}
	}
	m.runs = append(m.runs, Run{orig, out, olen, ulen})
	m.orig, m.out = orig+olen, out+ulen
}

// ToOrig 把输出偏移 o 映射回原文偏移；定义域 [0, OutLen()]。
func (m *Map) ToOrig(o int) int {
	m.probe = 0
	if o >= m.out {
		return m.orig
	}
	lo, hi := 0, len(m.runs)
	for lo < hi {
		m.probe++
		mid := (lo + hi) / 2
		r := m.runs[mid]
		switch {
		case o < r.Out:
			hi = mid
		case o >= r.Out+r.ULen:
			lo = mid + 1
		default:
			if r.OLen == 0 {
				return r.Orig
			}
			return r.Orig + min(o-r.Out, r.OLen-1)
		}
	}
	return m.orig
}

// ToOut 把原文偏移 i 映射到输出偏移；定义域 [0, OrigLen()]。
// 被删除的原文字节向前折叠到下一个输出位置（见 DESIGN.md 推导 2）。
func (m *Map) ToOut(i int) int {
	m.probe = 0
	if i >= m.orig {
		if n := len(m.runs); n > 0 {
			if l := m.runs[n-1]; l.OLen == 0 && l.Orig == i {
				return l.Out // 末尾合成字节：终点偏移落在它上面
			}
		}
		return m.out
	}
	lo, hi := 0, len(m.runs)
	for lo < hi {
		m.probe++
		mid := (lo + hi) / 2
		r := m.runs[mid]
		switch {
		case i < r.Orig:
			hi = mid
		case i >= r.Orig+r.OLen:
			lo = mid + 1
		default:
			if r.ULen == 0 {
				return r.Out
			}
			return r.Out + min(i-r.Orig, r.ULen-1)
		}
	}
	return m.out
}

// TruncateOut 把输出截断到 nu 字节：被截掉的区间转为删除区间（ULen=0）。
func (m *Map) TruncateOut(nu int) {
	r := m.runs
	end := len(r)
	for end > 0 && r[end-1].Out >= nu {
		end--
	}
	tail := append([]Run(nil), r[end:]...)
	r = r[:end]
	if end > 0 {
		if l := &r[end-1]; l.Out+l.ULen > nu { // 跨截断点的 1:1 区间：劈开
			keep := nu - l.Out
			tail = append([]Run{{Orig: l.Orig + keep, Out: nu, OLen: l.OLen - keep}}, tail...)
			l.OLen, l.ULen = keep, keep
		}
	}
	for j := range tail {
		tail[j].Out, tail[j].ULen = nu, 0
	}
	m.runs = append(r, tail...)
	m.out = nu
}

// Runs 返回全部区间（调用方不得修改），供 par 平移拼接。
func (m *Map) Runs() []Run { return m.runs }

// Len 返回区间数。
func (m *Map) Len() int { return len(m.runs) }

// LastProbe 返回最近一次 ToOrig/ToOut 检查的区间数。
func (m *Map) LastProbe() int { return m.probe }

// OutLen 返回输出总长。OrigLen 返回原文总长。
func (m *Map) OutLen() int  { return m.out }
func (m *Map) OrigLen() int { return m.orig }
