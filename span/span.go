// Package span 记录原文区间与输出区间的对应关系，支持双向查询。
// 映射表由两类区间组成：复制区间（n>0，原文 n 字节 1:1 映射到输出）
// 与删除区间（n<0，原文 -n 字节被删除，不产生输出）。区间数只随
// 删除点个数增长，与输出字节数无关。不依赖其他包。
package span

type seg struct {
	orig, out int // 区间在原文/输出中的起始偏移
	n         int // >0：复制长度；<0：删除长度
}

// Map 是只能追加的偏移映射。Copy/Drop 必须按原文偏移递增的顺序调用。
// 单个 Map 不要求并发安全。
type Map struct {
	segs    []seg
	out     int // 当前输出长度
	orig    int // 当前已记录的原文长度
	checked int // 最近一次 ToOrig/ToOut 检查的区间数
}

// Copy 记录原文偏移 pos 处的 1 字节被复制到输出末尾。
func (m *Map) Copy(pos int) { m.copyRun(pos, 1) }

func (m *Map) copyRun(pos, n int) {
	if k := len(m.segs); k > 0 && m.segs[k-1].n > 0 && m.segs[k-1].orig+m.segs[k-1].n == pos {
		m.segs[k-1].n += n
	} else {
		m.segs = append(m.segs, seg{pos, m.out, n})
	}
	m.out += n
	m.orig = pos + n
}

// Drop 记录原文 [pos, pos+n) 被删除（位于当前输出末尾处）。
func (m *Map) Drop(pos, n int) {
	if n == 0 {
		return
	}
	if k := len(m.segs); k > 0 && m.segs[k-1].n < 0 && m.segs[k-1].orig-m.segs[k-1].n == pos {
		m.segs[k-1].n -= n
	} else {
		m.segs = append(m.segs, seg{pos, m.out, -n})
	}
	m.orig = pos + n
}

// Retract 把输出末尾的 n 个复制字节（其原文偏移为 origs，递增）改为删除，
// 用于 Close 时应用末尾换行策略。调用方保证输出末尾恰有 n 个复制字节。
func (m *Map) Retract(n int, origs []int) {
	for n > 0 {
		s := &m.segs[len(m.segs)-1]
		if s.n <= n {
			n -= s.n
			m.out -= s.n
			m.segs = m.segs[:len(m.segs)-1]
		} else {
			s.n -= n
			m.out -= n
			n = 0
		}
	}
	if k := len(m.segs); k > 0 {
		s := m.segs[k-1]
		if s.n > 0 {
			m.orig = s.orig + s.n
		} else {
			m.orig = s.orig - s.n
		}
	} else {
		m.orig = 0
	}
	for _, p := range origs {
		m.Drop(p, 1)
	}
}

// Append 把另一个映射（原文坐标已为全局坐标）拼接到当前映射末尾。
func (m *Map) Append(o *Map) {
	for _, s := range o.segs {
		if s.n > 0 {
			m.copyRun(s.orig, s.n)
		} else {
			m.Drop(s.orig, -s.n)
		}
	}
}

// find 返回最后一个满足 f 的区间下标，并记录检查的区间数。
func (m *Map) find(f func(seg) bool) int {
	m.checked = 0
	lo, hi := 0, len(m.segs)
	for lo < hi {
		m.checked++
		mid := (lo + hi) / 2
		if f(m.segs[mid]) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo - 1
}

// ToOut 把原文偏移 i 映射到输出偏移。被删字节映射到删除点的输出位置。
func (m *Map) ToOut(i int) int {
	if i >= m.orig {
		m.checked = 0
		return m.out
	}
	s := m.segs[m.find(func(s seg) bool { return s.orig <= i })]
	if s.n > 0 {
		return s.out + i - s.orig
	}
	return s.out
}

// ToOrig 把输出偏移 o 映射回原文偏移。
func (m *Map) ToOrig(o int) int {
	if o >= m.out {
		m.checked = 0
		return m.orig
	}
	s := m.segs[m.find(func(s seg) bool { return s.out <= o })]
	return s.orig + o - s.out
}

// Checked 返回最近一次 ToOrig/ToOut 查询检查的区间数。
func (m *Map) Checked() int { return m.checked }

// Segs 返回映射表的区间数。
func (m *Map) Segs() int { return len(m.segs) }

// OutLen 返回输出总字节数。
func (m *Map) OutLen() int { return m.out }

// OrigLen 返回原文总字节数。
func (m *Map) OrigLen() int { return m.orig }
